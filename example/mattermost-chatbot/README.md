# mattermost-chatbot

`mattermost-chatbot` is a standalone Mattermost bot example for `pi-agent-go`.

It demonstrates:

- authenticating to Mattermost with a bot token
- listening for `posted` events over WebSocket
- replying to direct messages
- replying to `@bot` mentions in channels
- keeping one in-memory `prebuilt.ChatAgent` per thread or DM session
- exposing local tools for web search, web fetch, page metadata, link extraction, time lookup, and safe arithmetic

## Usage

Set the Mattermost connection details through flags or environment variables:

```powershell
$env:MATTERMOST_URL = "http://localhost:8065"
$env:MATTERMOST_TOKEN = "<your-bot-token>"
go run ./example/mattermost-chatbot --provider openai-codex --model gpt-5.4
```

Common flags:

- `--mattermost-url http://localhost:8065`
- `--mattermost-token <token>`
- `--auth-root ../pi-go`
- `--provider openai-codex`
- `--model gpt-5.4`
- `--system-prompt "You are a helpful Mattermost AI assistant."`

## Built-in Tools

Each chat session is created with the following local tools:

- `web_search`: search the public web with a built-in DuckDuckGo HTML flow
- `web_fetch`: fetch readable text from a public web page
- `web_page_meta`: fetch title, final URL, and description-like metadata
- `web_extract_links`: extract normalized outbound links from a page
- `get_time`: return the current UTC and local time, or a requested timezone
- `math_eval`: safely evaluate arithmetic expressions

`web_fetch`-style tools only allow public `http` and `https` URLs. Localhost, loopback, and private/internal addresses are intentionally blocked.

## Live Test Flow

1. Create or reuse a Mattermost bot account and copy its access token.
2. Add the bot to the team and channel where you want to test mentions.
3. Run the example locally.
4. Open a direct message with the bot and send a message.
5. In a public or private channel, send a message that starts with `@<bot-username>`.
6. Confirm the bot replies in the DM directly and replies in-thread for channel mentions.
