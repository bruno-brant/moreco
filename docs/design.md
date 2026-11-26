# moreco — design

moreco keeps per-repository editor configuration (starting with VS Code's
`.vscode/launch.json` and `.vscode/tasks.json`) in a private git repository
of your own, and restores it into any clone or worktree of the project.

## Problem

- Monorepos have many application roots, each with its own `.vscode/`.
  Some configuration is shared between them, but VS Code has no inheritance
  between folders' `launch.json`/`tasks.json` (no `extends`, no merging with a
  parent folder). Multi-root `.code-workspace` files and user-level
  `launch`/`tasks` give "global + local", not a merged shared layer.
- `git worktree add` does not bring untracked/ignored files, so `.vscode/` is
  missing in new worktrees. Symlinking breaks on absolute paths that point at
  the main checkout.
- Settings Sync covers user settings only, never workspace files.

## Concepts

**Store** — a git repository you control (e.g. a private GitHub repo), cloned
locally to `~/.cache/moreco/store`. It holds configuration for many projects:

```
store/
  moreco.yaml                      # manifest
  my-monorepo/
    _base/.vscode/launch.json      # shared layer, applied to every app root
    .vscode/launch.json            # repo-root layer
    apps/api/.vscode/launch.json   # app layer
    apps/web/.vscode/tasks.json
```

**Manifest** (`moreco.yaml`) maps repositories to store folders and lists the
managed file globs:

```yaml
repos:
  - folder: my-monorepo
    remotes: [github.com/acme/my-monorepo]   # normalized remote URLs
    rootCommit: 3f2a…                        # identity check / fallback
    files: [".vscode/launch.json", ".vscode/tasks.json"]
```

Repository identity is the normalized remote URL (`git@github.com:x/y.git` ≡
`https://github.com/x/y`), with the root commit hash
(`git rev-list --max-parents=0 HEAD`) as a stable cross-check: it is the same
in every clone, fork and worktree.

**Layers** — the file a worktree gets at `apps/api/.vscode/launch.json` is
`Resolve(_base/.vscode/launch.json, apps/api/.vscode/launch.json)`. Overlay
semantics (implemented in `internal/merge`):

- objects merge key by key;
- identity-keyed arrays merge item by item: `configurations`/`compounds` by
  `name`, `tasks` by `label`, `inputs` by `id`; new items are appended;
- other values replace;
- `{"$delete": true}` removes a key; `{"name": "X", "$delete": true}` removes
  an inherited item; `{"$replace": V}` replaces without merging.

**Path tokens** — on push, the worktree's absolute root path is rewritten to
`${workspaceFolder}` (or `{{repoRoot}}` where VS Code variables don't apply);
on pull, tokens are expanded for the current worktree.

## Worktree state

Each worktree records, in `.git/moreco-state.json` (per-worktree for linked
worktrees, via `git rev-parse --git-path`):

- the store commit it last synced with;
- for every managed file, the exact content moreco generated (the merge base).

## Commands (v1)

| Command | Behaviour |
|---|---|
| `moreco init <remote>` | Clone the store locally. |
| `moreco link [--name]` | Register the current repo in the manifest. |
| `moreco pull` | Fetch the store, resolve layers, expand paths, write files. With local edits, three-way merge (below). |
| `moreco push [--to-base]` | Fold local edits back into the layers, commit, push. |
| `moreco status` / `diff` | Show local vs. last sync vs. store. |
| `moreco resolve <file>` | Mark a conflicted file resolved. |
| `moreco hook install` | Install a `post-checkout` hook that pulls into new worktrees. |

### push — like `git push`

1. Fetch the store. If its HEAD is not the recorded sync commit, refuse:
   *store has moved on, run `moreco pull` first* (non-fast-forward).
2. For each managed file, `newLayer = Reconcile(lowerLayers, oldLayer, local)`:
   the app layer is rewritten so that resolving it again reproduces the local
   file exactly. Only what changed is touched, so values a layer pins on
   purpose stay pinned. Deleting an inherited item yields a `$delete` marker.
3. `--to-base` instead folds the changes into `_base` (shared by every app)
   and reconciles the app layer on top of the new base.
4. Commit (message names repo, branch, machine) and push. If the push races
   with another push, go back to step 1.

### pull — three-way merge

For each file: `base` = content generated at last sync, `ours` = file on disk,
`theirs` = freshly resolved from the store. `Merge3` walks objects by key and
keyed arrays by identity, so "I tweaked API's port" + "someone added Worker"
merges cleanly. Same value changed differently on both sides → conflict:
the local file is kept, the store's version is written to `<file>.theirs`,
and the file is marked conflicted; `push` refuses until `moreco resolve`.
(Git-style markers would make the JSON unloadable by VS Code.)

Files moreco created and you never edited are deleted when they disappear
from the store; edited ones are reported instead.

### hook

`git worktree add` runs `post-checkout` with an all-zero previous HEAD. The
hook detects that and runs `moreco pull`. A fresh worktree has no local edits,
so this pull cannot conflict. Hooks live in the shared `.git/hooks`, so one
install covers every worktree of the repo (or use a global `core.hooksPath`).
The hook never fails the checkout: if the store is unreachable it warns and
exits 0.

## Known limitations

- Comments in JSONC files are not preserved through the merge engine.
- Reordering items of a keyed array is stored as a `$replace` of the whole
  array in that layer, which stops it from inheriting base additions.
- Files may contain secrets (`.env` if added to the globs); the store is only
  as private as its remote. Encryption (age/sops) is a later addition.

## Build order

1. ✅ `internal/jsonc` — JSONC parse/format, ordered objects.
2. ✅ `internal/merge` — Merge/Resolve, Diff, Reconcile, Merge3, with
   randomized property tests.
3. Store + manifest + repo identity.
4. Path tokens.
5. CLI: init, link, pull, push, status/diff, resolve.
6. `hook install`.
