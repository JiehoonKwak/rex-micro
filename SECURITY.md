# Security

rex-micro reads the pad's keys (Input Monitoring) and can send shortcuts
(Accessibility), so treat a flaw that lets another program use those
permissions as serious.

Report a vulnerability privately through
[GitHub's private reporting](https://github.com/JiehoonKwak/rex-micro/security/advisories/new),
not in a public issue. Expect a reply within a week.

Releases are signed with one self-signed certificate (designated requirement
`certificate leaf = H"9e4be60b97fc1726491d4f1118eed7f518e0f9b5"`), published as
immutable GitHub releases with build attestations; see README for how to
verify a download.
