---
name: release
description: Cut a new stash release end-to-end - verify the repo is releasable, create the version tag and GitHub release, then watch the Docker Hub pipeline through to a published image. Use when the user asks to "release", "cut a release", "publish a new version", or "ship vX.Y.Z".
argument-hint: [X.Y.Z]
disable-model-invocation: true
allowed-tools: Bash, Read
---

# Release stash

A release is a git tag: there is no version file in the repo. Pushing a `vX.Y.Z` tag triggers `.github/workflows/release.yml`, which runs the quality gate and then builds and pushes the multi-arch Docker image to Docker Hub tagged `X.Y.Z`, `X.Y`, `X`, and `latest`.

## Inputs

- `$ARGUMENTS` is the new semver version `X.Y.Z` (no `v` prefix). If not provided, bump the patch number of the latest existing `v*` tag by 1 (e.g. `v0.1.4` → `0.1.5`). If no `v*` tag exists yet, propose `0.1.0`.

## Repo path

- **Repo root** (`REPO`): `git rev-parse --show-toplevel` from the current working directory; verify `go.mod` declares `module stash`.
- **Never use `cd` in a `Bash` call** — use `git -C "$REPO"` / `make -C "$REPO"` and absolute paths instead, so a stray working-directory change can't redirect later commands.

## Workflow

Run each step in order. Stop and report to the user on the first failure — do not continue, and do not attempt to "clean up" the user's working tree.

### Step 1 — Resolve the target version

Use the user-provided `X.Y.Z` if given. Otherwise:

```bash
git -C "$REPO" fetch --tags origin
git -C "$REPO" tag --list 'v*' --sort=-v:refname | head -1
```

Bump the patch number of the newest tag. Sanity-check the result is strictly greater than every existing tag.

### Step 2 — Preflight checks

Run each substep in order; stop on the first failure.

#### Step 2a — Branch is `main`

`git -C "$REPO" branch --show-current` must output `main`.

#### Step 2b — `main` is in sync with `origin/main`

Run `git -C "$REPO" fetch origin`, then confirm `main` and `origin/main` point at the same SHA. If local is behind or ahead, stop — the tag must point at a commit that is already on GitHub.

#### Step 2c — Working tree is clean

`git -C "$REPO" status --porcelain` must output nothing. Uncommitted work means the local checks in the next step wouldn't be testing the commit being released.

#### Step 2d — Run the local quality gate

`make -C "$REPO" ci` must succeed (fmt-check, vet, staticcheck, tidy-check, unit/integration tests, govulncheck, gosec, e2e typecheck). This is the same gate the release workflow runs — failing here saves a broken tag.

#### Step 2e — Run the browser e2e suite

`make -C "$REPO" test-e2e` must pass. CI does not run it, so this is the only place it gates a release.

#### Step 2f — Docker Hub secrets exist

`gh secret list` must show `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`. Without them the pipeline's login step fails after the tag is already pushed.

### Step 3 — Confirm the version with the user

Show the resolved version (and, if it was auto-bumped, the tag it was derived from) and wait for explicit approval before creating anything. This is the single confirmation for the whole flow — do not re-confirm between later steps.

### Step 4 — Create the GitHub release

```bash
gh release create "v$VERSION" --title "v$VERSION" --generate-notes --target main
```

- `gh release create` creates and pushes the tag itself — do not `git tag` / `git push --tags` separately.
- `--generate-notes` builds the changelog from commits/PRs since the previous tag. Don't write notes by hand.
- Surface the release URL that `gh` prints.

### Step 5 — Watch the release workflow

The tag triggers `release.yml` (quality gate job, then the multi-arch build-and-push job; allow ~5–10 minutes for the QEMU arm64 build). Poll:

```bash
gh run list --workflow release.yml --limit 1 --json status,conclusion,displayTitle,url
```

Proceed only once the run for `v$VERSION` shows `completed` / `success`. If it fails, surface the run URL and the failing job's logs (`gh run view <id> --log-failed`) — do not delete the tag or release without being asked.

### Step 6 — Verify the published image

Confirm the image actually landed on Docker Hub with both architectures. The Docker Hub username is stored only as a GitHub secret and cannot be read back — ask the user for it once if it isn't already known in the session, then:

```bash
docker buildx imagetools inspect "<user>/terminal-stash:$VERSION"
```

If `docker` isn't available, fall back to the registry API:

```bash
curl -sf "https://hub.docker.com/v2/repositories/<user>/terminal-stash/tags/$VERSION"
```

### Step 7 — Report the release summary

Print a short summary:

- Version released and the commit SHA it points at
- GitHub release URL
- Release workflow run URL and conclusion
- Docker image reference (`<user>/terminal-stash:$VERSION`) and the platforms verified

## Guardrails

- Never run `git push --force`, `git reset --hard`, or pass `--no-verify` during this flow.
- The tag/release creation in Step 4 is the only irreversible action; everything before it must pass first.
- If the workflow fails after the tag exists, fixing forward (new patch release) is the default — deleting tags/releases only on explicit instruction.
