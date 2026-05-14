# Lumi Go Backend

Go implementation of the Lumi gateway server.

## Build

```bash
cd go-backend
go build -o lumi ./cmd/lumi
```

## Run

```bash
# Use config file
./lumi -config lumi.config.json -port 3000

# With external web directory
./lumi -web ../web/dist -port 3000

# Default (uses embedded web files if available)
./lumi

# Explicit server command
./lumi server -config lumi.config.json -port 3000

# CLI commands without opening the web UI
./lumi setup
./lumi cron list
```

## Configuration

See `lumi.config.example.json` for configuration options.

Config file locations (in order of priority):
1. Specified via `-config` flag
2. `./lumi.config.json`
3. `./lumi.json`
4. `~/.config/lumi/config.json`

## API Endpoints

- `GET /api/agents` - List available agents
- `POST /api/agents/update` - Update agent settings
- `GET /api/workspaces` - List workspaces
- `POST /api/workspaces/create` - Create workspace
- `GET /api/sessions` - List sessions
- `POST /api/sessions/new` - Create new session
- `GET /api/sessions/:id` - Get session details
- `DELETE /api/sessions/:id` - Delete session
- `POST /api/chat` - Chat (SSE stream)
- `POST /api/permission/confirm` - Confirm permission request

## Development

```bash
# Build for development
go build ./...

# Run with web directory
go run ./cmd/lumi -web ../web/dist
```
