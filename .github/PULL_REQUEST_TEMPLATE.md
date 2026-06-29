## Summary

<!-- Brief description of what this PR does -->

## Motivation

<!-- Why is this change needed? Link to related issue(s) if applicable -->

Closes #

## Changes

<!-- Bullet list of key changes -->

-

## Type of Change

- [ ] Bug fix (non-breaking change that fixes an issue)
- [ ] New feature (non-breaking change that adds functionality)
- [ ] Breaking change (fix or feature that would cause existing functionality to not work as expected)
- [ ] Performance improvement
- [ ] Refactoring (no functional changes)
- [ ] Documentation update
- [ ] CI/CD or infrastructure change
- [ ] Dependency update

## Testing

<!-- Describe how you tested these changes -->

- [ ] Unit tests added/updated
- [ ] Integration tests pass
- [ ] Manual testing performed (describe below)

## Checklist

- [ ] My code follows the project's code style
- [ ] I have added/updated tests for new functionality
- [ ] `make verify` is green (fmt-check + vet + lint + sec + vuln + validate-lenses + test-race + test-e2e + check-python-drift + test-python + build)
- [ ] I have updated documentation if needed (docs reflect the code exactly — no drift)
- [ ] My changes generate no new warnings
- [ ] I have checked for potential security implications

### If this PR adds/changes a tool, provider, or env var

- [ ] New env var added to **both** `.env.example` **and** `docs/DEPLOYMENT.md`
- [ ] New tool documented in `docs/TOOLS.md` (`## Tool N: \`name\``), annotated with `readOnlyAnnotations(...)` or `writeAnnotations(...)`, and added to `setupTestDeps()` + `expectedTools` so the drift gates exercise it
- [ ] Regenerated the Python client (`make gen-python-client`) and committed the result — the `python-drift` CI job and pre-commit hook fail otherwise
- [ ] Destructive behavior is a separate endpoint, not a flag on a read tool
- [ ] No hardcoded tool/provider counts or version numbers introduced (registry.go / go.mod are the sources of truth)
- [ ] Drift gates pass (`TestToolsDocMatchesRegistry`, `TestAllToolsHaveAnnotations`, `TestOutputSchemaMatchesResponse`, `TestToolDescriptionQuality`)

## Screenshots / Logs

<!-- If applicable, add screenshots or relevant log output -->

## Additional Notes

<!-- Any additional information reviewers should know -->
