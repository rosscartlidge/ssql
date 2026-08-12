# GitHub Repository Migration Plan

Reference: DFC057
Created: 2026-03-12
Last modified: 2026-03-12

[Back to Index](./README.md)

**Status:** Planning
**Date:** March 2026
**Purpose:** Back up and delete personal GitHub repos so they can be re-published through employer's GitHub org

## Overview

Five repos need to be deleted from `github.com/rosscartlidge`:
- `ssql` — active, public, v4.27.0, 100+ tags
- `autocli` — active, public, v4.3.3, ssql dependency
- `gogstools` — public, legacy
- `streamv2` — public, legacy (predecessor to ssql)
- `stream` — public, legacy (predecessor to streamv2)

Two repos have active local development (`ssql`, `autocli`) that must continue working offline until re-published under a new org.

## Step 1: Back Up All Repos

Create bare mirror clones in `/home/rossc/github`. Bare mirrors include all branches, tags, and refs — everything needed to recreate the repo exactly.

```bash
mkdir -p /home/rossc/github

for repo in ssql autocli gogstools streamv2 stream; do
  git clone --mirror git@github.com:rosscartlidge/$repo.git /home/rossc/github/$repo.git
  echo "Backed up $repo"
done
```

### Verify backups

```bash
for repo in ssql autocli gogstools streamv2 stream; do
  echo "=== $repo ==="
  git -C /home/rossc/github/$repo.git tag | wc -l
  git -C /home/rossc/github/$repo.git branch -a | wc -l
  echo ""
done
```

### Also back up GitHub-only metadata

Download issues, PRs, releases (if any) that aren't in git:

```bash
for repo in ssql autocli gogstools streamv2 stream; do
  echo "=== $repo ==="
  gh api repos/rosscartlidge/$repo/releases --paginate > /home/rossc/github/$repo-releases.json 2>/dev/null
  gh api repos/rosscartlidge/$repo/issues --paginate > /home/rossc/github/$repo-issues.json 2>/dev/null
  echo "Saved metadata for $repo"
done
```

## Step 2: Update Local Development to Work Without GitHub

### 2a: ssql — add `replace` directive for local autocli

ssql's `go.mod` imports `github.com/rosscartlidge/autocli/v4 v4.3.3`. Once the GitHub repo is deleted, `go mod download` will fail. Add a local replace directive so development continues:

```bash
cd /home/rossc/src/ssql

# Add replace directive pointing to local autocli checkout
go mod edit -replace github.com/rosscartlidge/autocli/v4=/home/rossc/src/autocli
```

This makes `go build` use the local autocli source instead of fetching from GitHub. The go module cache already has the dependency cached, but this ensures it works even after cache expiry.

**go.mod will look like:**
```
module github.com/rosscartlidge/ssql/v4

go 1.26

require github.com/rosscartlidge/autocli/v4 v4.3.3

replace github.com/rosscartlidge/autocli/v4 => /home/rossc/src/autocli
```

### 2b: Remove GitHub remote from both repos

```bash
# ssql
cd /home/rossc/src/ssql
git remote rename origin old-github
# Keep the remote reference (renamed) so we don't lose the tracking info
# Can remove entirely later: git remote remove old-github

# autocli
cd /home/rossc/src/autocli
git remote rename origin old-github
```

This prevents accidental pushes to a non-existent repo and avoids confusing error messages. We rename instead of remove so the URL is preserved for reference when setting up the new remote later.

### 2c: Verify local development works

```bash
cd /home/rossc/src/ssql

# Build should work with local autocli
go build ./cmd/ssql/...

# Tests should pass
go test ./...

# ssql binary should work
./ssql version
```

### 2d: What still works after deletion

| Operation | Works? | Notes |
|-----------|--------|-------|
| `go build` | Yes | Local source + replace directive |
| `go test` | Yes | All local |
| `git commit` | Yes | Local repo unchanged |
| `git push` | No | Remote deleted — will re-add when new org ready |
| `go install github.com/rosscartlidge/ssql/...@latest` | No | Public repo gone |
| Other users importing ssql | No | Module proxy cache will serve for a while, then fail |

## Step 3: Delete GitHub Repos

**⚠️ DESTRUCTIVE — verify backups first!**

Before deleting, confirm each backup is complete:

```bash
# Compare local repo tag count with backup
for repo in ssql autocli; do
  local_tags=$(git -C /home/rossc/src/$repo tag | wc -l)
  backup_tags=$(git -C /home/rossc/github/$repo.git tag | wc -l)
  echo "$repo: local=$local_tags backup=$backup_tags"
done

# For repos without local checkouts, compare GitHub with backup
for repo in gogstools streamv2 stream; do
  gh_tags=$(gh api repos/rosscartlidge/$repo/tags --paginate -q '.[].name' | wc -l)
  backup_tags=$(git -C /home/rossc/github/$repo.git tag | wc -l)
  echo "$repo: github=$gh_tags backup=$backup_tags"
done
```

Once verified, delete:

```bash
for repo in ssql autocli gogstools streamv2 stream; do
  gh repo delete rosscartlidge/$repo --yes
  echo "Deleted $repo"
done
```

### Verify deletion

```bash
for repo in ssql autocli gogstools streamv2 stream; do
  gh repo view rosscartlidge/$repo 2>&1 | head -1
done
# Should show "Could not resolve to a Repository" for all
```

## Step 4: Re-Publishing (Future — May Not Happen)

This step is separate and may or may not happen depending on employer approval. It is included here for reference only.

If the repos are re-published under a new GitHub org (e.g., `github.com/neworg`), the following would be needed:

- Push from bare mirror backups to new org repos
- Update Go module paths (`rosscartlidge` → new org) in `go.mod`, all `*.go` files, and all `doc/*.md`
- Remove the `replace` directive added in Step 2a
- Update local git remotes to point to new org
- Tag a new release and verify `go install` works from the new location

## Checklist

- [ ] Back up all 5 repos to `/home/rossc/github/` (bare mirrors)
- [ ] Back up GitHub metadata (issues, releases)
- [ ] Add `replace` directive to ssql go.mod for local autocli
- [ ] Rename remotes in ssql and autocli
- [ ] Verify `go build` and `go test` work locally
- [ ] Verify backup tag/branch counts match
- [ ] Delete 5 repos from GitHub
- [ ] Verify deletion
