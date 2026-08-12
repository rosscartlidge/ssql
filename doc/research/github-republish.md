# GitHub Republish Plan

Reference: DFC061
Created: 2026-03-20
Last modified: 2026-03-20

[Back to Index](./README.md)

**Date:** 2026-03-19
**Purpose:** Push ssql and autocli back to GitHub after receiving employer open-source approval

## Prerequisites

- GitHub account `rosscartlidge` — authenticated via `gh` CLI
- Local repos at `/home/rossc/src/ssql` and `/home/rossc/src/autocli` with full git history
- Old repos already deleted (March 2026)

## Step 1: Create autocli repo and push

autocli must go first because ssql depends on it.

```bash
# Create the repo (public, no template/init — we're pushing existing history)
gh repo create rosscartlidge/autocli --public --description "Declarative CLI framework for Go with tab completion, subcommands, and clause support"

# Add the new remote
cd /home/rossc/src/autocli
git remote add origin git@github.com:rosscartlidge/autocli.git

# Push all branches and tags
git push origin --all
git push origin --tags
```

### Verify autocli

```bash
# Check it's visible
gh repo view rosscartlidge/autocli

# Check Go module proxy picks it up (may take a minute)
GOPROXY=direct go install github.com/rosscartlidge/autocli/v4@latest
```

If `go install` fails, force the proxy to fetch it:
```bash
GOPROXY=https://proxy.golang.org go list -m github.com/rosscartlidge/autocli/v4@latest
```

## Step 2: Update ssql's go.mod

Remove the local `replace` directive that was added when GitHub was deleted.

```bash
cd /home/rossc/src/ssql

# Remove the replace directive
go mod edit -dropreplace github.com/rosscartlidge/autocli/v4

# Verify go.mod looks right (should have require but no replace)
cat go.mod

# Verify it still builds (will fetch autocli from GitHub now)
go build ./cmd/ssql/...
go test ./cmd/ssql/...

# Commit the change
git add go.mod go.sum
git commit -m "fix: remove local replace directive for autocli (GitHub repos restored)"
```

## Step 3: Create ssql repo and push

```bash
# Create the repo
gh repo create rosscartlidge/ssql --public --description "SQL-style stream processing for the command line and Go"

# Add the new remote
cd /home/rossc/src/ssql
git remote add origin git@github.com:rosscartlidge/ssql.git

# Push all branches and tags
git push origin --all
git push origin --tags
```

### Verify ssql

```bash
# Check it's visible
gh repo view rosscartlidge/ssql

# Check Go module proxy
GOPROXY=direct go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest

# Verify the installed binary works
ssql version
```

## Step 4: Clean up old remote references

```bash
# Remove the old-github remote from both repos
cd /home/rossc/src/ssql
git remote remove old-github

cd /home/rossc/src/autocli
git remote remove old-github
```

## Step 5: Update CLAUDE.md

Remove the "GitHub Status (CRITICAL)" section that says repos are deleted and DO NOT push. Replace with normal workflow.

In `/home/rossc/src/ssql/CLAUDE.md`, delete the section:
```
## GitHub Status (CRITICAL)

**The GitHub repos have been deleted (March 2026).** Until further notice:
- **DO NOT** attempt to `git push` or interact with GitHub remotes
- The `origin` remote has been renamed to `old-github` — it points to a deleted repo
- Local development continues normally ...
```

And remove the related bullet from the `go.mod` notes:
```
- `go.mod` has a `replace` directive pointing autocli to `/home/rossc/src/autocli`
```

## Step 6: Republish legacy repos

Restore the 3 legacy repos from bare mirror backups:

```bash
for repo in gogstools streamv2 stream; do
  gh repo create rosscartlidge/$repo --public
  cd /home/rossc/github/$repo.git
  git push --mirror git@github.com:rosscartlidge/$repo.git
done
```

## Checklist

- [ ] Create `rosscartlidge/autocli` on GitHub
- [ ] Push autocli (all branches + tags)
- [ ] Verify `go install` works for autocli
- [ ] Remove `replace` directive from ssql's go.mod
- [ ] Commit the go.mod change
- [ ] Create `rosscartlidge/ssql` on GitHub
- [ ] Push ssql (all branches + tags)
- [ ] Verify `go install` works for ssql
- [ ] Remove `old-github` remote from both repos
- [ ] Update CLAUDE.md (remove no-push warning, remove replace directive note)
- [ ] Republish legacy repos (gogstools, streamv2, stream) from bare backups

## Troubleshooting

**`go install` fails with 404:**
The Go module proxy caches aggressively. Use `GOPROXY=direct` to bypass it, or force a fetch:
```bash
GOPROXY=https://proxy.golang.org go list -m github.com/rosscartlidge/ssql/v4@latest
```

**`go build` fails after removing replace directive:**
The autocli module may not be on the proxy yet. Wait a minute and retry, or use `GOPROXY=direct go mod download`.

**Push rejected (non-fast-forward):**
The repos are brand new (empty). This shouldn't happen. If it does, the repo was created with a README or .gitignore — delete and recreate without initialization:
```bash
gh repo delete rosscartlidge/REPO --yes
gh repo create rosscartlidge/REPO --public
```

**Tags missing after push:**
`git push origin --tags` only pushes tags that point to commits on pushed branches. If some tags are orphaned, push them individually:
```bash
git push origin v4.28.0
```
