# KubeZap Release Runbook

This document describes the end-to-end process for cutting a KubeZap release. All steps are required unless marked optional.

---

## Prerequisites

- `goreleaser` installed (`go install github.com/goreleaser/goreleaser/v2@latest`)
- `operator-sdk` installed and on `$PATH`
- Docker logged in to GHCR (`docker login ghcr.io -u <github-user> --password-stdin`)
- `GITHUB_TOKEN` environment variable set with `repo` + `write:packages` scopes
- Clean `main` branch (all §16 pre-public items resolved)

---

## 1. Decide the version

KubeZap follows [Semantic Versioning](https://semver.org/). Determine the next version:

| Change type | Bump |
|---|---|
| Breaking CRD or API change | MAJOR |
| New trigger type, new step action, new CRD field | MINOR |
| Bug fix, doc fix, security patch | PATCH |

> **Pre-1.0:** Use `v0.MINOR.PATCH`. Breaking changes increment MINOR.

---

## 2. Update version strings

Replace `<NEW>` with the target version (e.g. `0.4.0`):

```bash
VERSION=<NEW>

# Makefile default version
sed -i "s/^VERSION ?= .*/VERSION ?= $VERSION/" Makefile

# Helm chart
sed -i "s/^version: .*/version: $VERSION/" charts/kubezap-operator/Chart.yaml
sed -i "s/^appVersion: .*/appVersion: \"$VERSION\"/" charts/kubezap-operator/Chart.yaml

# OLM CSV
sed -i "s/name: kubezap.v.*/name: kubezap.v$VERSION/" bundle/manifests/kubezap.clusterserviceversion.yaml
sed -i "s/version: .*/version: $VERSION/" bundle/manifests/kubezap.clusterserviceversion.yaml
# Update spec.replaces to point to the previous version:
sed -i "s/replaces: kubezap.v.*/replaces: kubezap.v<PREV>/" bundle/manifests/kubezap.clusterserviceversion.yaml
```

---

## 3. Update CHANGELOG.md

Add a new section at the top of `CHANGELOG.md`:

```markdown
## [v<NEW>] - YYYY-MM-DD

### Added
- ...

### Changed
- ...

### Fixed
- ...
```

Commit the version bumps and changelog together:

```bash
git add Makefile charts/kubezap-operator/Chart.yaml bundle/manifests/kubezap.clusterserviceversion.yaml CHANGELOG.md
git commit -m "chore: bump version to v$VERSION"
git push origin main
```

---

## 4. Regenerate OLM bundle

```bash
make bundle
git add bundle/
git commit -m "chore: regenerate OLM bundle for v$VERSION"
git push origin main
```

Verify the bundle still passes validation:

```bash
operator-sdk bundle validate ./bundle
```

**Known operator-sdk quirk**: `operator-sdk generate kustomize manifests` (part of `make bundle`) deterministically drops the `Trigger` CRD's `resources`/`specDescriptors` from `config/manifests/bases/kubezap.clusterserviceversion.yaml` every time it runs — `Flow`/`FlowRun`/`Integration`'s survive correctly, only `Trigger`'s doesn't, for reasons not fully root-caused against operator-sdk v1.42.0. After every `make bundle`, manually re-add this block to `bundle/manifests/kubezap.clusterserviceversion.yaml`'s `Trigger` entry under `customresourcedefinitions.owned` before committing:

```yaml
      resources:
      - kind: Deployment
        version: v1
      - kind: Service
        version: v1
      specDescriptors:
      - description: Trigger type (webhook, cron, kafka, amqp, nats, resource).
        displayName: Type
        path: type
      - description: Reference to the Flow this Trigger executes when it fires.
        displayName: Flow Reference
        path: flowRef
      - description: Webhook trigger configuration (endpoint path, auth, rate limits).
        displayName: Webhook
        path: webhook
      - description: Kafka trigger configuration (topic, consumer group, integration ref).
        displayName: Kafka
        path: kafka
```

Re-run `operator-sdk bundle validate ./bundle` after the manual edit to confirm it's still valid.

---

## 5. Tag and push

```bash
git tag -a "v$VERSION" -m "KubeZap v$VERSION"
git push origin "v$VERSION"
```

This triggers two GitHub Actions workflows automatically:

- **`.github/workflows/release.yml`** — GoReleaser builds CLI binaries (`kubezap` + `kubectl-kubezap`) for linux/darwin/windows amd64+arm64, creates a GitHub Release with the binaries and a changelog, and pushes versioned container images to GHCR.
- _(`:latest` images were already pushed by `publish-latest.yml` on the preceding commit to `main`.)_

Monitor the workflow run:

```bash
gh run watch --repo kubezap/kubezap-operator
```

---

## 6. Verify the release

```bash
# CLI binaries available
gh release view "v$VERSION" --repo kubezap/kubezap-operator

# Container images pushed
docker pull ghcr.io/kubezap/controller:$VERSION
docker pull ghcr.io/kubezap/webhook-gateway:$VERSION

# Helm chart values reflect new version
grep appVersion charts/kubezap-operator/Chart.yaml
```

---

## 7. Publish Helm chart (manual until CI is wired)

Until the Helm chart publish step is automated in CI:

```bash
# OCI push (recommended)
helm package charts/kubezap-operator
helm push kubezap-operator-$VERSION.tgz oci://ghcr.io/kubezap/charts

# Verify
helm show chart oci://ghcr.io/kubezap/charts/kubezap-operator --version $VERSION
```

> **Note:** The first time, create the OCI package as public in the `kubezap` GitHub org package settings.

---

## 8. OperatorHub PR (for releases that change the CSV)

1. Fork [community-operators](https://github.com/k8s-operatorhub/community-operators) (or [community-operators-prod](https://github.com/redhat-openshift-ecosystem/community-operators-prod) for OpenShift).
2. Copy the `bundle/` directory to `operators/kubezap/v$VERSION/`.
3. Open a PR following the OperatorHub contribution guide.

---

## 9. Post-release checklist

- [ ] GitHub Release published with correct binaries and changelog
- [ ] `ghcr.io/kubezap/controller:$VERSION` and `:latest` pullable
- [ ] Helm chart installable (`helm install kubezap oci://ghcr.io/kubezap/charts/kubezap-operator --version $VERSION`)
- [ ] `kubezap version` reports `v$VERSION`
- [ ] OperatorHub PR opened (if CSV changed)
- [ ] Announce in project channels (if applicable)
