# KubeZap Release Runbook

This document describes the end-to-end process for cutting a KubeZap release. All steps are required unless marked optional.

**Cutting a release candidate (RC) first is optional, not mandatory** — use one whenever you want a real validation pass before the final tag goes out. **Recommended** for a MINOR or MAJOR release (new surface area — trigger types, CRD fields, breaking changes) or any patch you're not fully confident in (e.g. a security fix touching a code path you can't easily reason about in isolation). **Safe to skip** for a small, well-understood PATCH release (a doc fix, a narrowly-scoped bug fix) — go straight to [step 1](#1-decide-the-version) → final tag in that case, skipping the RC-specific sections below. If you do cut an RC, promoting it to final only ever re-tags that same validated commit, never new, untested code — see [Promoting an RC to Final](#promoting-an-rc-to-final).

---

## Prerequisites

- `goreleaser` installed (`go install github.com/goreleaser/goreleaser/v2@latest`)
- `operator-sdk` installed and on `$PATH`
- Docker logged in to GHCR (`docker login ghcr.io -u <github-user> --password-stdin`)
- `GITHUB_TOKEN` environment variable set with `repo` + `write:packages` scopes
- Clean `main` branch (all pre-public-release items resolved)

---

## Release Candidates vs. Final Releases

| | Release Candidate | Final Release |
|---|---|---|
| Tag | `v<X.Y.Z>-rc.<N>` (e.g. `v0.1.0-rc.1`, `v0.1.0-rc.2`) | `v<X.Y.Z>` (e.g. `v0.1.0`) |
| GitHub Release | Marked **prerelease** automatically (`goreleaser`'s `release.prerelease: auto`, `.goreleaser.yaml`) | Marked as the latest full release |
| `:latest` container tag | **Never** — RCs only ever get their own version tag | Moves to point at this version |
| Helm chart / OLM CSV version | `X.Y.Z-rc.N` | `X.Y.Z` |
| CHANGELOG.md | Not touched — the entry for the target version is already written before the first RC and doesn't change per RC unless RC testing surfaces something the changelog needs corrected | Date on the version's existing heading is set/confirmed to the actual release date |
| OperatorHub PR | Never | Only when the CSV changed, and only once the release itself is otherwise final — see [OperatorHub PR](#9-operatorhub-pr-final-releases-only) |

**Whether to cut an RC at all**: see the note at the top of this document — MINOR/MAJOR or higher-risk PATCH releases, go through an RC; a small, well-understood PATCH release can skip straight to the final tag using [step 1](#1-decide-the-version) through [step 5](#5-tag-and-push) with the plain final version from the start (skip [step 6](#6-validate-the-rc) and [Promoting an RC to Final](#promoting-an-rc-to-final) entirely).

**When to cut a new RC vs. promote to final** (once you've decided to use one): if RC testing finds something that needs a code change, fix it, commit, and cut `rc.<N+1>` from the new commit. Only promote to final once an RC has been validated with **no further code changes** — promotion re-tags the exact commit the last RC was built from, it never introduces new changes (see [Promoting an RC to Final](#promoting-an-rc-to-final)).

---

## 1. Decide the version

KubeZap follows [Semantic Versioning](https://semver.org/). Determine the next version:

| Change type                                      | Bump  |
| ------------------------------------------------ | ----- |
| Breaking CRD or API change                       | MAJOR |
| New trigger type, new step action, new CRD field | MINOR |
| Bug fix, doc fix, security patch                 | PATCH |

> **Pre-1.0:** Use `v0.MINOR.PATCH`. Breaking changes increment MINOR.

If you're using an RC for this release (see the note above — optional), decide the RC number: `rc.1` for the first candidate of this version, incrementing only if a later RC is needed after a code change (see above). If you're skipping the RC, just use the plain version below.

---

## 2. Update version strings

Replace `<NEW>` with the target version — including the `-rc.N` suffix if you're cutting a candidate (e.g. `0.1.0-rc.1`), or the plain version if going straight to final (e.g. `0.1.0`) — and `<PREVIOUS>` with the previous *final* version being replaced (e.g. `0.0.0` — omit `spec.replaces` entirely if there is no previous final release yet, see the note below):

```bash
VERSION=<NEW>
PREV=<PREVIOUS>

# Makefile default version
sed -i "s/^VERSION ?= .*/VERSION ?= $VERSION/" Makefile

# Helm chart — version and appVersion both carry the RC suffix while a release
# candidate is out; Helm's own semver handling means a prerelease chart version
# is correctly excluded from `helm search`/dependency resolution unless a
# client explicitly opts in with `--devel`.
sed -i "s/^version: .*/version: $VERSION/" charts/kubezap-operator/Chart.yaml
sed -i "s/^appVersion: .*/appVersion: \"$VERSION\"/" charts/kubezap-operator/Chart.yaml
```

> **No previous release yet (this project's first release)**: skip the `spec.replaces` line entirely — `make bundle` (next step) does not add one unless it already exists in the base CSV, and OLM's own convention is that a channel's first entry has no `replaces` field. Only set `spec.replaces` from the second release onward.

---

## 3. Regenerate OLM bundle

`make bundle` reads `VERSION` from the Makefile (just updated above) and regenerates `bundle/manifests/kubezap.clusterserviceversion.yaml`'s `name`/`version` fields — don't hand-edit the CSV directly.

```bash
make bundle
```

Review the diff. If a `spec.replaces` line was carried over from a previous release, confirm it still points at the correct prior *final* version (RCs are never a `replaces` target).

---

## 4. Confirm CHANGELOG.md

`CHANGELOG.md`'s entry for the target version should already exist (written up front, describing everything going into this release) — an RC does not get its own changelog section.

- **If you're cutting an RC**: read the entry over and correct anything RC testing revealed was wrong, but don't change the version heading or add a date yet — the date is set at [final promotion](#promoting-an-rc-to-final).
- **If you're skipping the RC**: read the entry over, correct anything that needs it, and set the real release date on the `## [v<X.Y.Z>]` heading now — this tag is the final release, there's no later promotion step to do it in.

Commit the version-bump and bundle regeneration together:

```bash
git add Makefile charts/kubezap-operator/Chart.yaml bundle/ CHANGELOG.md
git commit -m "chore: prepare v$VERSION"
git push origin main
```

(Open this as a normal PR and merge it first if your workflow requires review before `main` — the commit above assumes direct push is acceptable for a version-bump-only change; adjust to match how this repo actually merges to `main`.)

---

## 5. Tag and push

```bash
git tag -a "v$VERSION" -m "KubeZap v$VERSION"
git push origin "v$VERSION"
```

(`$VERSION` is whatever you set in [step 1](#1-decide-the-version) — with the `-rc.N` suffix if you're cutting a candidate, e.g. `v0.1.0-rc.1`, or the plain version if going straight to final, e.g. `v0.1.0`.)

This triggers the same two workflows either way — see [Release Candidates vs. Final Releases](#release-candidates-vs-final-releases) for what differs in their output:

- **`.github/workflows/release.yml`** — GoReleaser builds CLI binaries (`kubezap` + `kubectl-kubezap`), creates a GitHub Release (automatically marked **prerelease** for an `-rc.N` tag), and pushes container images to GHCR tagged with the exact version — `:latest` moves only for a plain final tag, never for an `-rc.N` one.
- **`release-helm`** job publishes the Helm chart to `oci://ghcr.io/kubezap/charts` under this version.

**If you skipped the RC**, this tag *is* the final release — skip ahead to [step 7](#7-verify-the-release) (no [validation](#6-validate-the-rc) or [promotion](#promoting-an-rc-to-final) step needed, since there's no separate RC artifact to validate first).

Monitor the workflow run:

```bash
gh run watch --repo kubezap/kubezap-operator
```

---

## 6. Validate the RC

Before promoting, actually exercise the RC — this is the entire point of having one:

- [ ] Install from the RC's Helm chart / OLM bundle / raw manifests into a real cluster (Kind/k3s) and confirm the operator comes up healthy.
- [ ] Run through the golden-path smoke test for each trigger type you're shipping in this release.
- [ ] `kubezap version` (from the RC's CLI binary) reports the RC version string.
- [ ] Confirm `ghcr.io/kubezap/controller:latest` was **not** moved — it should still point at the previous final release (or not exist yet, for this project's first release).

If anything fails, fix it, commit, and go back to [step 1](#1-decide-the-version) for `rc.<N+1>`. If it all passes, proceed to promotion.

---

## Promoting an RC to Final

Promotion **rebuilds from the same commit the last validated RC was built from** — it does not introduce any new code. If you need a code change, that's a new RC, not a promotion.

1. On the exact commit the last-good RC's tag points at, bump version strings again, this time to the plain final version (no `-rc.N` suffix) — repeat [step 2](#2-update-version-strings) and [step 3](#3-regenerate-olm-bundle) with `VERSION=<X.Y.Z>` (no suffix).
2. Set the real release date on `CHANGELOG.md`'s `## [v<X.Y.Z>]` heading (it should already exist per [step 4](#4-confirm-changelogmd) — just confirm/correct the date).
3. Commit and push to `main` exactly as in [step 4](#4-confirm-changelogmd):
   ```bash
   git add Makefile charts/kubezap-operator/Chart.yaml bundle/ CHANGELOG.md
   git commit -m "chore: promote v$VERSION to final"
   git push origin main
   ```
4. Tag and push the final version:
   ```bash
   git tag -a "v$VERSION" -m "KubeZap v$VERSION"
   git push origin "v$VERSION"
   ```
   GoReleaser marks this GitHub Release as the latest full release (no `-rc.N` in the tag → `prerelease: auto` resolves to `false`), and the container-image workflow moves `:latest` to this version.
5. Continue with [step 7 onward](#7-verify-the-release) below, using the final version.

---

## 7. Verify the release

```bash
# CLI binaries available
gh release view "v$VERSION" --repo kubezap/kubezap-operator

# Container images pushed
docker pull ghcr.io/kubezap/controller:$VERSION
docker pull ghcr.io/kubezap/webhook-gateway:$VERSION

# Helm chart values reflect new version
grep appVersion charts/kubezap-operator/Chart.yaml
```

For a final release only, also confirm `:latest` now resolves to this version:

```bash
docker pull ghcr.io/kubezap/controller:latest
docker inspect ghcr.io/kubezap/controller:latest --format '{{index .RepoTags}}'
```

---

## 8. Verify the published Helm chart

```bash
helm show chart oci://ghcr.io/kubezap/charts/kubezap-operator --version $VERSION
```

(For an RC version, `--version` must be given explicitly — Helm won't surface a prerelease chart version by default.)

---

## 9. OperatorHub PR (final releases only)

Never open this for an RC. This step is also independent of — and currently blocked behind — the operator's own release-readiness gate (see the `EPIC-001`/`STORY-007` tracking for current status); don't open an OperatorHub PR just because a final tag shipped without checking that gate first.

1. Fork [community-operators](https://github.com/k8s-operatorhub/community-operators) (or [community-operators-prod](https://github.com/redhat-openshift-ecosystem/community-operators-prod) for OpenShift).
2. Copy the `bundle/` directory to `operators/kubezap/v$VERSION/`.
3. Open a PR following the OperatorHub contribution guide.

---

## 10. Post-release checklist

**After cutting an RC:**
- [ ] GitHub Release published and marked **Pre-release**
- [ ] `ghcr.io/kubezap/controller:$VERSION` (the RC version) pullable; `:latest` unchanged
- [ ] Helm chart installable with `--version $VERSION` (and `helm install`'s prerelease guard confirmed — it should refuse without an explicit version)
- [ ] RC validated per [step 6](#6-validate-the-rc)

**After promoting to final:**
- [ ] GitHub Release published and **not** marked prerelease
- [ ] `ghcr.io/kubezap/controller:$VERSION` and `:latest` both pullable and identical
- [ ] Helm chart installable without `--devel`
- [ ] `kubezap version` reports `v$VERSION`
- [ ] OperatorHub PR opened (if CSV changed, and only once the release-readiness gate above is actually clear)
