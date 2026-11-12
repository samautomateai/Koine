# ADR 0009 — AWS SSO, assumed roles and web identity on the standard library

- Status: accepted
- Date: 2026-11-12
- Requirements: REQ-LLM-017
- Supersedes in part: [ADR 0008](0008-cloud-credentials-without-sdks.md) (the limit of the AWS chain to static keys)

## Context

ADR 0008 kept Bedrock's credential chain to what needs no further call: a Bedrock API key, environment keys and static keys in a profile.
Most people who use Bedrock at work sign in through IAM Identity Center (`aws sso login`) or assume a role from a base profile, and the only
workaround was to export short-lived keys by hand. Pi relies on the AWS SDK chain and documents profiles, environment keys, ECS task
credentials and IRSA (web identity).

## Decision

Widen the chain, still without an SDK (ADR 0004): `core/llm.AWSSource` reads `~/.aws/config` and `~/.aws/credentials` (INI), and resolves a
profile that holds static keys, an SSO login (`sso_session` with an `[sso-session]` section, or the older `sso_start_url` form), a role
(`role_arn` with `source_profile`, to any depth, or `credential_source = Environment`) or a web identity token file. The calls are made with
`net/http`: STS `AssumeRole` signed with the SigV4 code already used for Bedrock, STS `AssumeRoleWithWebIdentity` unsigned, and the SSO portal's
`GetRoleCredentials` with the access token that `aws sso login` cached under `~/.aws/sso/cache`. Koine never starts an SSO login or refreshes
that token: when it is missing or expired the error says to run `aws sso login --profile NAME`. Temporary credentials are cached until five
minutes before they expire. Not supported, and refused with a message that names the setting: `credential_process` (it runs a helper program,
which would need the trust rules of ADR 0002 and a decision of its own), `mfa_serial` (no way to ask for a code), and the ECS and EC2 instance
credential endpoints.

## Consequences

Users on SSO or roles work without exporting keys, and the code stays readable in `core/llm` (about 400 lines). We own the protocol details
(the SSO cache file name is the SHA-1 of the session name or start URL, as the AWS CLI writes it; STS answers XML), checked against fake servers
only, never a live account. An explicit `AWS_PROFILE` that names a role or SSO profile now beats `AWS_ACCESS_KEY_ID` in the environment, which
then only serves as the source of `credential_source = Environment`; otherwise environment keys still win, as before.
