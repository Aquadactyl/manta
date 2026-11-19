# Manta security policy

Manta is the Aquadactyl server daemon, maintained by Euphoria Development and
derived from Pterodactyl Wings. This policy covers the Aquadactyl/Manta repository.

## Reporting a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/Aquadactyl/manta/security/advisories/new)
to contact the Manta maintainers. Private reporting is enabled. Do not open a
public issue or PR containing an exploit, unpatched vulnerability, credentials
or private user data.

Include the exact Manta version or commit, host OS and architecture, deployment
type, affected endpoint or operation, impact, prerequisites and minimal
reproduction steps from an environment you control. Review and redact evidence.
Never attach a full node `config.yml`, node/API tokens, TLS private keys, session
cookies, server files or a database dump.

Panel vulnerabilities belong in
[Aquadactyl's private report form](https://github.com/Aquadactyl/aquadactyl/security/advisories/new).
Use the [public issue forms](https://github.com/Aquadactyl/manta/issues/new/choose)
for ordinary bugs, requests and help after reading
[Manta common issues](https://aquadactyl.uk/docs/manta/troubleshooting).

## Supported releases and disclosure

Current releases are public betas. Security fixes target the
[latest published Manta release](https://github.com/Aquadactyl/manta/releases)
and the default development branch. Include the original affected version even
if it is older; when possible, check whether it also occurs in a current release.
Manta and Panel versions are independent.

Maintainers assess reports privately, coordinate fixes and publication, and use
GitHub security advisories when appropriate. Keep details private while that
coordination is in progress. Do not test against installations without their
operator's permission.

If a report also affects upstream Wings or a dependency, include that information
so maintainers can coordinate with the relevant project. The
[upstream security policy](https://github.com/pterodactyl/wings/security/policy)
belongs to Pterodactyl; it is not Manta's reporting contact.

## Deployment boundaries

Manta controls Docker and game-server files. Keep its node token, configuration,
Docker socket and TLS keys private, and grant access only to trusted operators.
Game containers, third-party eggs, extensions and host configuration need their
own review. Diagnostics may include endpoints and logs: review them before sharing.
See the installation and troubleshooting guides for native Linux, container and
macOS configurations.
