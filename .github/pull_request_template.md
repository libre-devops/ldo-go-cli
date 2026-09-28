## Summary

Brief description of the change.

## Type of Change

- [ ] Bug fix
- [ ] Parity with the Python ldo
- [ ] Enhancement / improvement
- [ ] Documentation update
- [ ] Refactor
- [ ] CI / pipeline change
- [ ] Dependency update

## Testing

Describe how this was tested.

- [ ] `just ci` passes locally (gofmt, vet, tests with coverage, staticcheck, govulncheck)
- [ ] New behaviour is covered by tests that use the fakes, not the network or a real `az`
- [ ] A changed JSON shape is recorded (`just record-json`) and in the changelog
- [ ] Compared with the Python `ldo`, where it has the same command (say how)
- [ ] Manually run against a real tenant (say which commands)

## Checklist

- [ ] Code follows project conventions (see CONTRIBUTING.md and AI.md)
- [ ] `docs/` updated for any new command, option or exit code
- [ ] `docs/differences.md` updated for any new difference from the Python `ldo`
- [ ] CHANGELOG.md updated
- [ ] No tokens, tenant ids, subscription ids or real host names included (gitleaks checks)
