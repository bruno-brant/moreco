# moreco — design

moreco keeps per-repository editor configuration (starting with VS Code's
`.vscode/launch.json` and `.vscode/tasks.json`) in a private git repository
of your own, and restores it into any clone or worktree of the project.

## Problem

- `git worktree add` does not bring untracked/ignored files, so `.vscode/` is
  missing in new worktrees. Symlinking breaks on absolute paths that point at
  the main checkout.
- Settings Sync covers user settings only, never workspace files.
- Monorepos have many application roots, each with its own `.vscode/`, so the
  set of files to carry around is large and easy to lose track of.

## Principles

- **Files are opaque.** moreco never parses managed files. They are stored and
  restored byte for byte, so comments, formatting and key order (JSONC is the
  native format of these files) always survive. The only transformation is
  path tokens (below), which is plain text substitution.
- **Git-like semantics.** `push` refuses when the store has moved on; `pull`
  merges into local edits instead of overwriting them.

## Concepts

**Store** — a git repository you control (e.g. a private GitHub repo), cloned
locally to the user cache directory (`~/.cache/moreco/store` on Linux;
override with `$MORECO_STORE`). The clone is a disposable cache: every
command resets it to the remote first, and a push that loses a race is rolled
back. A lock file next to it serializes concurrent runs (e.g. two worktree
hooks). It holds configuration for many projects,
one folder per project, mirroring the project's tree:

```
store/
  moreco.yaml                      # manifest
  my-monorepo/
    .vscode/launch.json            # repo root
    apps/api/.vscode/launch.json
    apps/web/.vscode/tasks.json
```

**Manifest** (`moreco.yaml`) maps repositories to store folders and lists the
managed file globs:

```yaml
repos:
  - folder: my-monorepo
    remotes: [github.com/acme/my-monorepo]   # normalized remote URLs
    rootCommit: 3f2a…                        # identity check / fallback
    files: ["**/.vscode/launch.json", "**/.vscode/tasks.json"]
```

Matching a checkout to a folder: a listed remote wins (and its root commit
must agree, catching a URL reused for another project); otherwise the root
commit alone (the checkout's remotes can then be added with `moreco link`).
Shallow clones report no root commit, since their history is cut.

Repository identity is the normalized remote URL (`git@github.com:x/y.git` ≡
`https://github.com/x/y`), with the root commit hash
(`git rev-list --max-parents=0 HEAD`) as a stable cross-check: it is the same
in every clone, fork and worktree. Several remotes may point at one folder.

**Path tokens** — on push, occurrences of the absolute root path of *any*
worktree of the repository are replaced with `{{moreco:root}}`; on pull, the
token is expanded to the current worktree's root. Matches must be whole path
prefixes (`/src/app` does not match `/src/app2`). Windows roots also have
`{{moreco:root:win}}` (backslashes) and `{{moreco:root:json}}` (escaped
backslashes) forms, matched case-insensitively. This fixes configs that hard-code the main checkout.
(Prefer `${workspaceFolder}` in the files themselves where VS Code supports it;
moreco leaves VS Code variables untouched.)

## Worktree state

Each worktree records, in `.git/moreco-state.json` (per-worktree for linked
worktrees, via `git rev-parse --git-path`):

- the store commit it last synced with;
- which files are currently conflicted.

The merge base for each file is the store's content at that commit
(`<commit>:<folder>/<file>`), read from the store's git history, so the state
file stays tiny.

**Which files are managed** — files under the worktree matching the
project's globs, except: files the project's own git tracks (already shared;
moreco reports them as skipped), and anything under `.git`, `node_modules`,
`.venv`, `.direnv`, `.terraform` or a nested checkout (a directory with its
own `.git`, e.g. worktrees kept inside the main checkout).

## Commands (v1)

| Command | Behaviour |
|---|---|
| `moreco init <remote>` | Clone the store locally. |
| `moreco link [--name]` | Register the current repo in the manifest. |
| `moreco pull` | Fetch the store and bring managed files up to date, merging with local edits. |
| `moreco push` | Copy managed files into the store, commit, push. |
| `moreco status` / `diff` | Show local vs. last sync vs. store. |
| `moreco resolve <file>` | Mark a conflicted file resolved. |
| `moreco hook install` | Install a `post-checkout` hook that pulls into new worktrees. |

### push — like `git push`

1. Refuse if any managed file is conflicted or still contains conflict markers.
2. Fetch the store. If this project's folder differs from what it was at the
   recorded sync commit, refuse: *run `moreco pull` first* (non-fast-forward).
   Pushes by other projects sharing the store do not count. A worktree that
   never synced may only push into an empty folder.
3. Copy every managed file (path tokens applied) into the project's store
   folder. A managed file that was synced before and is now gone locally is
   deleted from the store.
4. Commit (message names repo, branch, machine) and push. If the push races
   with another push, go back to step 2.
5. Record the new sync commit and contents.

### pull — three-way merge

For each file, with `base` = content at last sync, `ours` = file on disk,
`theirs` = file in the store:

| Situation | Result |
|---|---|
| ours = base | take theirs (write, or delete if gone from the store) |
| theirs = base, or ours = theirs | keep ours |
| both changed | `git merge-file ours base theirs` (line-based, like git) |
| deleted on one side, changed on the other | conflict |
| new on both sides, different | merge against an empty base |

A clean merge is written to disk. A conflict writes git-style markers into
the file — VS Code shows its merge-conflict actions (Accept Current / Incoming
/ Both) in any file — and marks it conflicted until `moreco resolve`. The file
is not valid JSONC while the markers are in it, exactly as with git.

Files moreco created and you never edited are deleted when they disappear
from the store; edited ones are reported instead.

### hook

`git worktree add` runs `post-checkout` with an all-zero previous HEAD. The
hook detects that and runs `moreco pull`. A fresh worktree has no local edits,
so this pull cannot conflict. Hooks live in the shared `.git/hooks`, so one
install covers every worktree of the repo (or use a global `core.hooksPath`).
The hook never fails the checkout: if the store is unreachable it warns and
exits 0.

## Deferred

- **Inheritance.** A shared base for many app roots. Cheapest form that keeps
  files opaque: whole-file fallback (an app root without its own `launch.json`
  gets `_base/.vscode/launch.json`). Key-level merging would require a
  comment-preserving JSONC editor (e.g. `github.com/tailscale/hujson`) and is
  out of scope for now.
- **Secrets.** Files may contain secrets if `.env`-like globs are added; the
  store is only as private as its remote. Encryption (age/sops) later.

## Build order

1. ✅ Store + manifest + repo identity (`internal/store`, `internal/manifest`,
   `internal/repoid`), with `moreco init`, `link` and `info`.
2. ✅ Path tokens (`internal/pathtok`).
3. ✅ Worktree state and the three-way file merge (`internal/worktree`,
   `internal/textmerge`).
4. ✅ CLI: pull, push, status, resolve (`internal/ops`). `diff` not yet.
5. `hook install`.
