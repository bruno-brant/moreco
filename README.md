# moreco

Keep your monorepo's editor configuration (`.vscode/launch.json`,
`.vscode/tasks.json`, …) in a private git repo and restore it into any clone
or git worktree. Files are stored byte for byte, so comments and formatting
survive; pulling merges with local edits like git does.

Status: early but usable: `init`, `link`, `pull`, `push`, `status`,
`resolve`. The worktree hook is next. See [docs/design.md](docs/design.md).

```bash
go install github.com/bruno-brant/moreco/cmd/moreco@latest
moreco init git@github.com:you/my-configs.git   # an empty private repo
cd ~/src/my-monorepo && moreco link
moreco push          # save .vscode files to the store

git worktree add ../my-monorepo-feature && cd ../my-monorepo-feature
moreco pull          # files restored, absolute paths pointing here
```

Absolute paths to any worktree of the repo are stored as `{{moreco:root}}`
and expanded on pull. Like git, `push` refuses when the store has changes you
haven't pulled, and `pull` merges them into your local edits; conflicts get
git-style markers, then `moreco resolve`.

Development: `go test ./...`
