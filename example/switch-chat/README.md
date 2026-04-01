# switch-chat

`switch-chat` is an example CLI for `pi-agent-go` that demonstrates:

- built-in preset switching
- per-preset local session persistence
- explicit reuse of `pi-go` credentials through `--auth-root`
- separate `chat` and `reflection` runtime modes

## Usage

```powershell
go run ./example/switch-chat --auth-root ../pi-go
```

Common flags:

- `--auth-root ../pi-go`: points to the directory that contains `.pigo/auth.json` and `.pigo/.env`
- `--provider openai-codex`
- `--model gpt-5.4`
- `--data-dir example/switch-chat/.data`
- `--preset chat`

Available commands:

- `/agents`
- `/use <preset>`
- `/show`
- `/reset`
- `/exit`

## Presets

- `chat`: general-purpose chat
- `coder`: code-focused chat
- `reflect`: single-run reflection mode

`reflect` intentionally does not restore a live runtime snapshot. The current
`prebuilt.ReflectionAgent` treats the first user message as the original request
for one reflection run, so replaying a whole transcript into it would change the
behavior. The example therefore persists only the visible transcript for that
mode.
