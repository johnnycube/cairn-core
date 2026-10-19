# Security policy

## Supported versions

The latest release (the newest `v*` tag and its `ghcr.io/opencairn-org/cairn-core` image). Older releases do not receive fixes.

## Reporting a vulnerability

Please do not open a public issue. Report suspected vulnerabilities privately through GitHub's
"Report a vulnerability" button on the Security tab, or by email to `security@johanneskueber.com`.
You will get an acknowledgement within 7 days and a fix or a decision within 30.

Vulnerabilities in the provider workers belong to their own repositories (cairn-provider-strava,
cairn-provider-garmin); vulnerabilities in Strava or Garmin services go to those vendors.

## Verifying images

Published images are signed keyless with cosign and carry SBOM and provenance attestations;
see the header of `.github/workflows/build-core.yml` for the `cosign verify` command.
