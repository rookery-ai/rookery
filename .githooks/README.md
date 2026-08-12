# Git hooks

Installed with `make hooks`, which points `core.hooksPath` here. Git does not
share hooks through a clone, so this is opt-in per checkout — `CONTRIBUTING.md`
names it in the setup block.

`patterns.txt` is read by BOTH `commit-msg` and the `pr-description` job in
`.github/workflows/pr.yml`, so local and CI enforcement cannot drift apart.
Add a pattern here and both surfaces gain it.

The patterns apply to **commit messages and PR descriptions only**, never to
file content. File content is covered by gitleaks and GitHub push protection.
That separation is why RFC1918 literals can be banned here without breaking the
connector YAML examples, which document self-hosted deployments and are files.
