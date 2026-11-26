# moreco

Keep your monorepo's editor configuration (`.vscode/launch.json`,
`.vscode/tasks.json`, …) in a private git repo and restore it into any clone
or git worktree. Files are stored byte for byte, so comments and formatting
survive; pulling merges with local edits like git does.

Status: early. The store works (`init`, `link`, `info`); `push`/`pull` are
next. See [docs/design.md](docs/design.md).

```bash
go install github.com/bruno-brant/moreco/cmd/moreco@latest
moreco init git@github.com:you/my-configs.git   # an empty private repo
cd ~/src/my-monorepo && moreco link
moreco info
```

Development: `go test ./...`
