package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	mmodel "github.com/mattermost/mattermost/server/public/model"
)

type fakeMattermostAPI struct {
	mu         sync.Mutex
	me         *mmodel.User
	channels   map[string]*mmodel.Channel
	posts      []*mmodel.Post
	getMeCalls int
}

func (f *fakeMattermostAPI) GetMe(_ context.Context, _ string) (*mmodel.User, *mmodel.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getMeCalls++
	if f.me == nil {
		return nil, nil, errors.New("missing user")
	}
	return f.me, nil, nil
}

func (f *fakeMattermostAPI) GetChannel(_ context.Context, channelID string) (*mmodel.Channel, *mmodel.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	channel := f.channels[channelID]
	if channel == nil {
		return nil, nil, errors.New("missing channel")
	}
	return channel, nil, nil
}

func (f *fakeMattermostAPI) CreatePost(_ context.Context, post *mmodel.Post) (*mmodel.Post, *mmodel.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cloned := *post
	f.posts = append(f.posts, &cloned)
	return &cloned, nil, nil
}

type fakeChatRunner struct {
	mu       sync.Mutex
	messages []string
	reply    string
}

func (f *fakeChatRunner) Chat(_ context.Context, message string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, message)
	return f.reply, nil
}

func TestBuildPostActionFiltersDMAndMentionTraffic(t *testing.T) {
	botID := "bot-user"
	username := "chatbot"

	dmAction := buildPostAction(&mmodel.Post{
		Id:        "post-1",
		UserId:    "user-1",
		ChannelId: "dm-1",
		Message:   "hello there",
	}, mmodel.ChannelTypeDirect, botID, username)
	if !dmAction.Accept {
		t.Fatal("expected direct message to be accepted")
	}
	if dmAction.Prompt != "hello there" {
		t.Fatalf("unexpected DM prompt %q", dmAction.Prompt)
	}

	channelAction := buildPostAction(&mmodel.Post{
		Id:        "post-2",
		UserId:    "user-1",
		ChannelId: "channel-1",
		Message:   "hello everyone",
	}, mmodel.ChannelTypeOpen, botID, username)
	if channelAction.Accept {
		t.Fatal("expected channel message without mention to be ignored")
	}

	mentionAction := buildPostAction(&mmodel.Post{
		Id:        "post-3",
		UserId:    "user-1",
		ChannelId: "channel-1",
		Message:   "@chatbot explain this",
	}, mmodel.ChannelTypeOpen, botID, username)
	if !mentionAction.Accept {
		t.Fatal("expected channel message with mention to be accepted")
	}
	if mentionAction.Prompt != "explain this" {
		t.Fatalf("unexpected mention prompt %q", mentionAction.Prompt)
	}

	selfAction := buildPostAction(&mmodel.Post{
		Id:        "post-4",
		UserId:    botID,
		ChannelId: "channel-1",
		Message:   "@chatbot should not run",
	}, mmodel.ChannelTypeOpen, botID, username)
	if selfAction.Accept {
		t.Fatal("expected bot-authored post to be ignored")
	}
}

func TestSessionKeyForPost(t *testing.T) {
	tests := []struct {
		name        string
		post        *mmodel.Post
		channelType mmodel.ChannelType
		want        string
	}{
		{
			name: "channel root creates thread session",
			post: &mmodel.Post{
				Id:        "post-root",
				ChannelId: "channel-1",
			},
			channelType: mmodel.ChannelTypeOpen,
			want:        "thread:post-root",
		},
		{
			name: "channel reply reuses root",
			post: &mmodel.Post{
				Id:        "post-reply",
				ChannelId: "channel-1",
				RootId:    "post-root",
			},
			channelType: mmodel.ChannelTypeOpen,
			want:        "thread:post-root",
		},
		{
			name: "dm without thread uses channel",
			post: &mmodel.Post{
				Id:        "post-dm",
				ChannelId: "dm-1",
			},
			channelType: mmodel.ChannelTypeDirect,
			want:        "dm-channel:dm-1",
		},
		{
			name: "dm thread uses root",
			post: &mmodel.Post{
				Id:        "post-dm-reply",
				ChannelId: "dm-1",
				RootId:    "root-1",
			},
			channelType: mmodel.ChannelTypeDirect,
			want:        "dm-thread:root-1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sessionKeyForPost(test.post, test.channelType); got != test.want {
				t.Fatalf("unexpected session key: got %q want %q", got, test.want)
			}
		})
	}
}

func TestSessionManagerReusesRunnerByKey(t *testing.T) {
	var (
		mu        sync.Mutex
		createCnt int
		runners   = map[string]*fakeChatRunner{}
	)

	manager := &sessionManager{
		sessions: map[string]*chatSession{},
		factory: func(key string) (chatRunner, error) {
			mu.Lock()
			defer mu.Unlock()
			createCnt++
			runner := &fakeChatRunner{reply: "ok"}
			runners[key] = runner
			return runner, nil
		},
	}

	if err := manager.WithSession(context.Background(), "thread:a", func(agent chatRunner) error {
		_, err := agent.Chat(context.Background(), "one")
		return err
	}); err != nil {
		t.Fatalf("first session call failed: %v", err)
	}
	if err := manager.WithSession(context.Background(), "thread:a", func(agent chatRunner) error {
		_, err := agent.Chat(context.Background(), "two")
		return err
	}); err != nil {
		t.Fatalf("second session call failed: %v", err)
	}
	if err := manager.WithSession(context.Background(), "thread:b", func(agent chatRunner) error {
		_, err := agent.Chat(context.Background(), "three")
		return err
	}); err != nil {
		t.Fatalf("third session call failed: %v", err)
	}

	if createCnt != 2 {
		t.Fatalf("unexpected factory call count: got %d want 2", createCnt)
	}
	if got := len(runners["thread:a"].messages); got != 2 {
		t.Fatalf("expected thread:a runner reuse, got %d messages", got)
	}
	if got := len(runners["thread:b"].messages); got != 1 {
		t.Fatalf("expected isolated thread:b runner, got %d messages", got)
	}
}

func TestStripLeadingMention(t *testing.T) {
	got := stripLeadingMention("   @chatbot, please help  ", "chatbot")
	if got != "please help" {
		t.Fatalf("unexpected stripped message %q", got)
	}

	unchanged := stripLeadingMention("please help @chatbot", "chatbot")
	if unchanged != "please help @chatbot" {
		t.Fatalf("unexpected non-leading mention strip %q", unchanged)
	}
}

func TestParsePostedEventSupportsStringPost(t *testing.T) {
	post := `{"id":"post-1","user_id":"user-1","channel_id":"channel-1","message":"hello"}`
	event := mmodel.NewWebSocketEvent(mmodel.WebsocketEventPosted, "team-1", "channel-1", "user-1", nil, "")
	event.Add("post", post)
	event.Add("channel_type", string(mmodel.ChannelTypeOpen))

	incoming, err := parsePostedEvent(event)
	if err != nil {
		t.Fatalf("parsePostedEvent returned error: %v", err)
	}
	if incoming.Post.Id != "post-1" {
		t.Fatalf("unexpected parsed post id %q", incoming.Post.Id)
	}
	if incoming.ChannelType != mmodel.ChannelTypeOpen {
		t.Fatalf("unexpected channel type %q", incoming.ChannelType)
	}
}

func TestNewAppRequiresMattermostConfig(t *testing.T) {
	_, err := NewApp(AppConfig{}, AppDeps{})
	if err == nil || !strings.Contains(err.Error(), "mattermost URL is required") {
		t.Fatalf("expected missing URL error, got %v", err)
	}

	_, err = NewApp(AppConfig{MattermostURL: "http://localhost:8065"}, AppDeps{})
	if err == nil || !strings.Contains(err.Error(), "mattermost token is required") {
		t.Fatalf("expected missing token error, got %v", err)
	}
}

func TestAppBootstrapUsesGetMe(t *testing.T) {
	api := &fakeMattermostAPI{
		me: &mmodel.User{
			Id:       "bot-user",
			Username: "chatbot",
		},
	}

	app, err := NewApp(AppConfig{
		MattermostURL:   "http://localhost:8065",
		MattermostToken: "token",
	}, AppDeps{
		API: api,
		ChatFactory: func(_ string) (chatRunner, error) {
			return &fakeChatRunner{reply: "ok"}, nil
		},
		SocketFactory: func(_, _ string) (mattermostSocket, error) {
			return nil, errors.New("unused in bootstrap test")
		},
	})
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}

	if err := app.bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap returned error: %v", err)
	}
	if app.botUser == nil || app.botUser.Username != "chatbot" {
		t.Fatalf("unexpected bot user: %+v", app.botUser)
	}
	if api.getMeCalls != 1 {
		t.Fatalf("expected GetMe to be called once, got %d", api.getMeCalls)
	}
}

func TestMattermostWebSocketURL(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "http://localhost:8065/", want: "ws://localhost:8065"},
		{input: "https://example.com/subpath/", want: "wss://example.com/subpath"},
		{input: "ws://localhost:8065", want: "ws://localhost:8065"},
		{input: "ftp://localhost:8065", wantErr: true},
	}

	for _, test := range tests {
		got, err := mattermostWebSocketURL(test.input)
		if test.wantErr {
			if err == nil {
				t.Fatalf("expected error for %q", test.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("unexpected websocket url for %q: got %q want %q", test.input, got, test.want)
		}
	}
}
