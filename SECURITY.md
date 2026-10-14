# Security policy

Only the latest release is maintained. Please report vulnerabilities privately
using GitHub's **Security → Report a vulnerability** if available. If private
reporting is unavailable, open an issue requesting a private contact without
including exploit details or secrets.

Never attach tokens, OAuth callback URLs, credential-store exports, personal data
or real batch plans. Revoke exposed tokens in Slack immediately.

Credentials are stored using Windows Credential Manager, macOS Keychain or Linux
Secret Service. There is no plaintext fallback. CI uses `SLACKADM_TOKEN`.
Profiles are local labels, not a security boundary. A process running as your OS
user can access the same credentials. Batch files contain workspace/user IDs and
selected channel state; store them privately and review before applying.
