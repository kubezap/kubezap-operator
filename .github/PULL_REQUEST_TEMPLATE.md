## What does this PR do, and why?

<!--
Briefly describe the change and the motivation behind it. Link any relevant
issue, Story, or design record (docs/design/) if one exists.
-->

## How was this tested?

<!-- Check what you ran and paste relevant output/results where useful. -->

- [ ] `make test` (unit tests via envtest)
- [ ] `make test-e2e` (E2E tests via Kind cluster)
- [ ] `make lint` (golangci-lint)
- [ ] Manual testing against a live cluster (describe steps/results below)

<!-- Manual testing notes, if applicable: -->

## Checklist

- [ ] I ran `gofmt -w .` and `goimports -w .` on all changed Go files
- [ ] If I changed types in `api/v1alpha1/`, I ran `make generate && make manifests`
      and committed the resulting generated files
- [ ] If I added/changed `+kubebuilder:rbac` markers, I confirmed the rule
      actually landed in `config/rbac/role.yaml` (and `namespaced_role.yaml`
      if applicable) after `make manifests`
- [ ] I added/updated Ginkgo tests covering this change
- [ ] I updated relevant docs (`docs/`) for any user-facing or API change
- [ ] This change does not require a design record (`docs/design/`), or I've
      added one per `docs/design/README.md`

## Additional context

<!-- Anything else reviewers should know: breaking changes, follow-up work, open questions, etc. -->
