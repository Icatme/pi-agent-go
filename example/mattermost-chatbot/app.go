package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	core "github.com/Icatme/pi-agent-go"
	"github.com/Icatme/pi-agent-go/prebuilt"
	mmodel "github.com/mattermost/mattermost/server/public/model"
)

const (
	initialReconnectDelay = time.Second
	maxReconnectDelay     = 30 * time.Second
)

type mattermostAPI interface {
	GetMe(ctx context.Context, etag string) (*mmodel.User, *mmodel.Response, error)
	GetChannel(ctx context.Context, channelID string) (*mmodel.Channel, *mmodel.Response, error)
	CreatePost(ctx context.Context, post *mmodel.Post) (*mmodel.Post, *mmodel.Response, error)
}

type mattermostSocket interface {
	Listen()
	Close()
	Events() <-chan *mmodel.WebSocketEvent
	Responses() <-chan *mmodel.WebSocketResponse
	PingTimeouts() <-chan bool
	ListenErr() *mmodel.AppError
}

type chatRunner interface {
	Chat(ctx context.Context, message string) (string, error)
}

type chatAgentFactory func(sessionKey string) (chatRunner, error)

type AppDeps struct {
	API           mattermostAPI
	SocketFactory func(url, token string) (mattermostSocket, error)
	ChatFactory   chatAgentFactory
}

type App struct {
	config        AppConfig
	api           mattermostAPI
	socketFactory func(url, token string) (mattermostSocket, error)
	sessions      *sessionManager
	botUser       *mmodel.User
	channelCache  map[string]mmodel.ChannelType
	channelMu     sync.RWMutex
	wg            sync.WaitGroup
}

type incomingPost struct {
	Post        *mmodel.Post
	ChannelType mmodel.ChannelType
}

type postAction struct {
	Accept     bool
	Prompt     string
	SessionKey string
	ReplyRoot  string
}

type chatSession struct {
	mu    sync.Mutex
	agent chatRunner
}

type sessionManager struct {
	mu       sync.Mutex
	factory  chatAgentFactory
	sessions map[string]*chatSession
}

type client4Adapter struct {
	client *mmodel.Client4
}

type webSocketClient struct {
	client *mmodel.WebSocketClient
}

func NewApp(config AppConfig, deps AppDeps) (*App, error) {
	var (
		err      error
		modelRef core.ModelRef
	)

	config, err = config.Normalize()
	if err != nil {
		return nil, err
	}

	modelRef, err = resolveModelRef(config.Provider, config.Model, config.AuthRoot)
	if err != nil {
		return nil, err
	}

	if deps.API == nil {
		client := mmodel.NewAPIv4Client(strings.TrimRight(config.MattermostURL, "/"))
		client.SetToken(config.MattermostToken)
		deps.API = client4Adapter{client: client}
	}
	if deps.SocketFactory == nil {
		deps.SocketFactory = func(url, token string) (mattermostSocket, error) {
			socketURL, err := mattermostWebSocketURL(url)
			if err != nil {
				return nil, err
			}
			client, err := mmodel.NewWebSocketClient4(socketURL, token)
			if err != nil {
				return nil, err
			}
			return webSocketClient{client: client}, nil
		}
	}
	if deps.ChatFactory == nil {
		deps.ChatFactory = newDefaultChatFactory(config, modelRef)
	}

	return &App{
		config:        config,
		api:           deps.API,
		socketFactory: deps.SocketFactory,
		sessions: &sessionManager{
			factory:  deps.ChatFactory,
			sessions: map[string]*chatSession{},
		},
		channelCache: map[string]mmodel.ChannelType{},
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	if err := a.bootstrap(ctx); err != nil {
		return err
	}

	fmt.Fprintf(a.config.Stdout, "mattermost-chatbot ready. bot=@%s provider=%s model=%s\n", a.botUser.Username, a.config.Provider, a.config.Model)
	defer a.wg.Wait()

	return a.runReconnectLoop(ctx)
}

func (a *App) bootstrap(ctx context.Context) error {
	user, _, err := a.api.GetMe(ctx, "")
	if err != nil {
		return fmt.Errorf("mattermost-chatbot: bootstrap failed: %w", err)
	}
	a.botUser = user
	return nil
}

func (a *App) runReconnectLoop(ctx context.Context) error {
	delay := initialReconnectDelay
	for {
		socket, err := a.socketFactory(a.config.MattermostURL, a.config.MattermostToken)
		if err != nil {
			a.logf("websocket connect failed: %v", err)
			if err := waitForReconnect(ctx, delay); err != nil {
				return nil
			}
			delay = nextReconnectDelay(delay)
			continue
		}

		delay = initialReconnectDelay
		err = a.consumeSocket(ctx, socket)
		socket.Close()
		if ctx.Err() != nil {
			return nil
		}

		a.logf("websocket disconnected: %v", err)
		if err := waitForReconnect(ctx, delay); err != nil {
			return nil
		}
		delay = nextReconnectDelay(delay)
	}
}

func (a *App) consumeSocket(ctx context.Context, socket mattermostSocket) error {
	socket.Listen()

	events := socket.Events()
	responses := socket.Responses()
	pingTimeouts := socket.PingTimeouts()

	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-pingTimeouts:
			if !ok {
				pingTimeouts = nil
				continue
			}
			return errors.New("mattermost websocket ping timeout")
		case _, ok := <-responses:
			if !ok {
				responses = nil
				return socketCloseError(socket)
			}
		case event, ok := <-events:
			if !ok {
				events = nil
				return socketCloseError(socket)
			}
			if event == nil || event.EventType() != mmodel.WebsocketEventPosted {
				continue
			}

			incoming, err := parsePostedEvent(event)
			if err != nil {
				a.logf("failed to parse posted event: %v", err)
				continue
			}

			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				if err := a.handleIncomingPost(ctx, incoming); err != nil {
					a.logf("failed to handle post %s: %v", incoming.Post.Id, err)
				}
			}()
		}
	}
}

func (a *App) handleIncomingPost(ctx context.Context, incoming incomingPost) error {
	channelType, err := a.resolveChannelType(ctx, incoming)
	if err != nil {
		return err
	}

	action := buildPostAction(incoming.Post, channelType, a.botUser.Id, a.botUser.Username)
	if !action.Accept {
		return nil
	}

	return a.sessions.WithSession(ctx, action.SessionKey, func(agent chatRunner) error {
		reply, err := agent.Chat(ctx, action.Prompt)
		if err != nil {
			return err
		}

		reply = strings.TrimSpace(reply)
		if reply == "" {
			return nil
		}

		post := &mmodel.Post{
			ChannelId: incoming.Post.ChannelId,
			RootId:    action.ReplyRoot,
			Message:   reply,
		}
		_, _, err = a.api.CreatePost(ctx, post)
		return err
	})
}

func (a *App) resolveChannelType(ctx context.Context, incoming incomingPost) (mmodel.ChannelType, error) {
	if isKnownChannelType(incoming.ChannelType) {
		a.channelMu.Lock()
		a.channelCache[incoming.Post.ChannelId] = incoming.ChannelType
		a.channelMu.Unlock()
		return incoming.ChannelType, nil
	}

	a.channelMu.RLock()
	cached := a.channelCache[incoming.Post.ChannelId]
	a.channelMu.RUnlock()
	if isKnownChannelType(cached) {
		return cached, nil
	}

	channel, _, err := a.api.GetChannel(ctx, incoming.Post.ChannelId)
	if err != nil {
		return "", fmt.Errorf("mattermost-chatbot: get channel %s: %w", incoming.Post.ChannelId, err)
	}

	a.channelMu.Lock()
	a.channelCache[incoming.Post.ChannelId] = channel.Type
	a.channelMu.Unlock()
	return channel.Type, nil
}

func parsePostedEvent(event *mmodel.WebSocketEvent) (incomingPost, error) {
	if event == nil {
		return incomingPost{}, errors.New("nil websocket event")
	}
	if event.EventType() != mmodel.WebsocketEventPosted {
		return incomingPost{}, fmt.Errorf("unexpected websocket event %q", event.EventType())
	}

	data := event.GetData()
	post, err := decodePostedEventPost(data["post"])
	if err != nil {
		return incomingPost{}, err
	}

	return incomingPost{
		Post:        post,
		ChannelType: decodeEventChannelType(data["channel_type"]),
	}, nil
}

func decodePostedEventPost(raw any) (*mmodel.Post, error) {
	switch typed := raw.(type) {
	case nil:
		return nil, errors.New("posted event missing post")
	case *mmodel.Post:
		return typed, nil
	case string:
		var post mmodel.Post
		if err := json.Unmarshal([]byte(typed), &post); err != nil {
			return nil, fmt.Errorf("decode posted event string post: %w", err)
		}
		return &post, nil
	case map[string]any:
		body, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("marshal posted event map post: %w", err)
		}
		var post mmodel.Post
		if err := json.Unmarshal(body, &post); err != nil {
			return nil, fmt.Errorf("decode posted event map post: %w", err)
		}
		return &post, nil
	default:
		return nil, fmt.Errorf("unsupported posted event post type %T", raw)
	}
}

func decodeEventChannelType(raw any) mmodel.ChannelType {
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return mmodel.ChannelType(value)
}

func buildPostAction(post *mmodel.Post, channelType mmodel.ChannelType, botUserID, botUsername string) postAction {
	if post == nil {
		return postAction{}
	}
	if strings.TrimSpace(post.UserId) == "" || post.UserId == botUserID {
		return postAction{}
	}
	if isSystemPost(post) {
		return postAction{}
	}

	message := strings.TrimSpace(post.Message)
	if message == "" {
		return postAction{}
	}

	if channelType == mmodel.ChannelTypeDirect {
		return postAction{
			Accept:     true,
			Prompt:     message,
			SessionKey: sessionKeyForPost(post, channelType),
			ReplyRoot:  replyRootForPost(post, channelType),
		}
	}

	if !containsMention(message, botUsername) {
		return postAction{}
	}

	prompt := strings.TrimSpace(stripLeadingMention(message, botUsername))
	if prompt == "" {
		return postAction{}
	}

	return postAction{
		Accept:     true,
		Prompt:     prompt,
		SessionKey: sessionKeyForPost(post, channelType),
		ReplyRoot:  replyRootForPost(post, channelType),
	}
}

func sessionKeyForPost(post *mmodel.Post, channelType mmodel.ChannelType) string {
	if channelType == mmodel.ChannelTypeDirect {
		if strings.TrimSpace(post.RootId) != "" {
			return "dm-thread:" + post.RootId
		}
		return "dm-channel:" + post.ChannelId
	}

	rootID := strings.TrimSpace(post.RootId)
	if rootID == "" {
		rootID = post.Id
	}
	return "thread:" + rootID
}

func replyRootForPost(post *mmodel.Post, channelType mmodel.ChannelType) string {
	if channelType == mmodel.ChannelTypeDirect {
		return strings.TrimSpace(post.RootId)
	}
	if strings.TrimSpace(post.RootId) != "" {
		return post.RootId
	}
	return post.Id
}

func isSystemPost(post *mmodel.Post) bool {
	return strings.HasPrefix(post.Type, mmodel.PostSystemMessagePrefix)
}

func containsMention(message, username string) bool {
	token := "@" + strings.ToLower(strings.TrimSpace(username))
	if token == "@" {
		return false
	}

	lower := strings.ToLower(message)
	offset := 0
	for {
		index := strings.Index(lower[offset:], token)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(token)
		if isMentionBoundary(lower, start-1) && isMentionBoundary(lower, end) {
			return true
		}
		offset = end
	}
}

func stripLeadingMention(message, username string) string {
	token := "@" + strings.TrimSpace(username)
	trimmed := strings.TrimSpace(message)
	if token == "@" || len(trimmed) < len(token) {
		return trimmed
	}
	if !strings.EqualFold(trimmed[:len(token)], token) || !isMentionBoundary(trimmed, len(token)) {
		return trimmed
	}

	return strings.TrimLeftFunc(trimmed[len(token):], func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(":,-", r)
	})
}

func isMentionBoundary(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[index:])
	if unicode.IsSpace(r) {
		return true
	}
	return strings.ContainsRune(".,:;!?()[]{}<>\"'`", r)
}

func isKnownChannelType(channelType mmodel.ChannelType) bool {
	switch channelType {
	case mmodel.ChannelTypeDirect, mmodel.ChannelTypeGroup, mmodel.ChannelTypeOpen, mmodel.ChannelTypePrivate:
		return true
	default:
		return false
	}
}

func waitForReconnect(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextReconnectDelay(current time.Duration) time.Duration {
	next := current * 2
	if next > maxReconnectDelay {
		return maxReconnectDelay
	}
	return next
}

func mattermostWebSocketURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("mattermost-chatbot: invalid Mattermost URL %q: %w", rawURL, err)
	}

	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("mattermost-chatbot: unsupported Mattermost URL scheme %q", parsed.Scheme)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func socketCloseError(socket mattermostSocket) error {
	if err := socket.ListenErr(); err != nil {
		return err
	}
	return errors.New("mattermost websocket closed")
}

func (a *App) logf(format string, args ...any) {
	fmt.Fprintf(a.config.Stderr, "mattermost-chatbot: "+format+"\n", args...)
}

func (m *sessionManager) WithSession(ctx context.Context, key string, fn func(chatRunner) error) error {
	session, err := m.getOrCreate(key)
	if err != nil {
		return err
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	return fn(session.agent)
}

func (m *sessionManager) getOrCreate(key string) (*chatSession, error) {
	m.mu.Lock()
	existing := m.sessions[key]
	m.mu.Unlock()
	if existing != nil {
		return existing, nil
	}

	agent, err := m.factory(key)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing = m.sessions[key]; existing != nil {
		return existing, nil
	}

	session := &chatSession{agent: agent}
	m.sessions[key] = session
	return session, nil
}

func cloneModelRef(ref core.ModelRef) core.ModelRef {
	cloned := ref
	if len(ref.ProviderConfig.Headers) > 0 {
		cloned.ProviderConfig.Headers = make(map[string]string, len(ref.ProviderConfig.Headers))
		for key, value := range ref.ProviderConfig.Headers {
			cloned.ProviderConfig.Headers[key] = value
		}
	}
	if ref.ProviderConfig.Auth != nil {
		auth := *ref.ProviderConfig.Auth
		if ref.ProviderConfig.Auth.OAuth != nil {
			oauth := *ref.ProviderConfig.Auth.OAuth
			auth.OAuth = &oauth
		}
		cloned.ProviderConfig.Auth = &auth
	}
	if len(ref.Metadata) > 0 {
		cloned.Metadata = make(map[string]any, len(ref.Metadata))
		for key, value := range ref.Metadata {
			cloned.Metadata[key] = value
		}
	}
	return cloned
}

func newDefaultChatFactory(config AppConfig, modelRef core.ModelRef) chatAgentFactory {
	httpClient := newRestrictedHTTPClient()
	definition := newChatAgentDefinition(config, modelRef, toolRuntimeConfig{
		Client:        httpClient,
		SearchBaseURL: defaultDuckDuckGoSearchURL,
		Now:           time.Now,
		LocalLocation: time.Local,
	})

	return func(_ string) (chatRunner, error) {
		return prebuilt.NewChatAgent(definition)
	}
}

func newChatAgentDefinition(config AppConfig, modelRef core.ModelRef, runtimeConfig toolRuntimeConfig) core.AgentDefinition {
	return core.AgentDefinition{
		SystemPrompt: config.SystemPrompt,
		DefaultModel: cloneModelRef(modelRef),
		Tools:        buildDefaultTools(newToolRuntime(runtimeConfig)),
	}
}

func (c client4Adapter) GetMe(ctx context.Context, etag string) (*mmodel.User, *mmodel.Response, error) {
	return c.client.GetMe(ctx, etag)
}

func (c client4Adapter) GetChannel(ctx context.Context, channelID string) (*mmodel.Channel, *mmodel.Response, error) {
	return c.client.GetChannel(ctx, channelID)
}

func (c client4Adapter) CreatePost(ctx context.Context, post *mmodel.Post) (*mmodel.Post, *mmodel.Response, error) {
	return c.client.CreatePost(ctx, post)
}

func (w webSocketClient) Listen() {
	w.client.Listen()
}

func (w webSocketClient) Close() {
	w.client.Close()
}

func (w webSocketClient) Events() <-chan *mmodel.WebSocketEvent {
	return w.client.EventChannel
}

func (w webSocketClient) Responses() <-chan *mmodel.WebSocketResponse {
	return w.client.ResponseChannel
}

func (w webSocketClient) PingTimeouts() <-chan bool {
	return w.client.PingTimeoutChannel
}

func (w webSocketClient) ListenErr() *mmodel.AppError {
	return w.client.ListenError
}
