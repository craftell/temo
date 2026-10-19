# Repository instructions

- Use poteto-mode for development and independently review changes before delivery.
- Update the relevant verification skill, feature recipes, and regression tests in the same PR as behavior changes. Preserve existing verification history and distinguish executed checks from authored tests.
- Include the intended next release version in VERSION and release notes in the same feature or fix PR. Do not open a version-only follow-up PR. Reconcile the version against main and published tags before merging concurrent PRs.
- VERSION is release preparation. Source builds retain module/VCS/development identity. Official release builds derive their version from the matching Git tag.
- Tagging, publishing, and merging require their own authorization. Never create a release merely to test the updater.
- If CLAUDE.md is introduced, its entire content must be @AGENTS.md.
