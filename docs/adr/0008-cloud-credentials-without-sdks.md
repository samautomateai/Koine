# ADR 0008 — Bedrock and Vertex without cloud SDKs, with a short credential chain

- Status: accepted
- Date: 2026-11-12
- Requirements: REQ-LLM-014; REQ-LLM-015

## Context

Amazon Bedrock needs requests signed with AWS Signature Version 4 and answers streams in a binary framing; Google Vertex AI needs an OAuth access token made from a service-account key or a gcloud login. The official SDKs do this and much more (SSO, role assumption, instance metadata, web identity), but they are large third-party dependencies, and ADR 0004 keeps the core on the standard library. Users who sign in to a cloud provider differ widely in how.

## Decision

Implement the protocols on the standard library: SigV4 (`crypto/hmac`, `crypto/sha256`), the event stream framing (`encoding/binary`, `hash/crc32`) and an RS256 JWT for Google (`crypto/rsa`, `crypto/x509`). Limit the credential chains to what needs no further service call or helper program: for AWS a Bedrock API key, access keys in the environment and static keys in a shared-file profile; for Google an API key, a service-account key file and an authorized-user file. Profiles that need SSO, role assumption, `credential_process` or web identity are refused with a message that names the alternatives. Metadata servers (EC2, ECS, Compute Engine) are not read.

## Consequences

No dependency, a small attack surface, and every byte that is signed or sent can be read in `core/llm`. The cost is that we own the protocols: SigV4 is checked against AWS's published test case and key-derivation example, the event stream against frames we build ourselves, and neither against a live service. Users on SSO or assumed roles must export short-lived keys (`aws configure export-credentials`) or use a Bedrock API key until the chain is widened; doing so would be a new requirement, and anything that runs a helper program (`credential_process`) would also need to respect the trust rules of ADR 0002.
