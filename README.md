# moreco

Keep your monorepo's editor configuration (`.vscode/launch.json`,
`.vscode/tasks.json`, …) in a private git repo, with a shared base layer
merged into every application root, and restore it into any clone or git
worktree.

Status: early. The JSONC and merge engine are in place; the CLI is next.
See [docs/design.md](docs/design.md).

```bash
go test ./...
```
