# moreco

Keep your monorepo's editor configuration (`.vscode/launch.json`,
`.vscode/tasks.json`, …) in a private git repo and restore it into any clone
or git worktree. Files are stored byte for byte, so comments and formatting
survive; pulling merges with local edits like git does.

Status: design. See [docs/design.md](docs/design.md).
