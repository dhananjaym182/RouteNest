# RouteNest Domain Glossary

RouteNest exposes logical models through configured provider instances while keeping
provider credentials, client access and routing policy distinct.

## Language

**Provider Definition**:
Immutable knowledge about a provider offering: stable identity, protocol,
authentication methods, declared capabilities and default public metadata. It contains
no user credentials or live health state.

**Provider Instance**:
One configured, routable upstream. Multiple Provider Instances may share a Provider
Definition while using different credentials, endpoints, limits or network policy;
built-in upstreams are Provider Instances too.

**Provider Connection**:
A stored non-Cline login or imported-token relationship with a provider. A Provider
Connection may be bound to a Provider Instance, but creating one does not itself create
or save a Provider Instance. Native OpenCode Zen configuration is not automatically a
Provider Connection.

**Provider Credential**:
Authentication material and identity state used by one or more Provider Instances,
including API keys, Cline account credentials, OAuth accounts, refresh tokens,
imported bearer tokens and provider sessions.
_Avoid_: API key or credential when it is unclear whether the value authenticates an
upstream provider or a RouteNest client.

**Cline Account**:
One authenticated Cline identity in RouteNest's built-in account pool. Deleting a
Cline Account does not mean deleting Provider Connections, client keys or model
configuration.

**Client API Key**:
An inbound credential accepted from a RouteNest client when calling `/v1/*`. It is not
a Provider Credential and never grants administrator access.

**Administrator Recovery Code**:
A single-use, 256-bit credential that proves possession for resetting the reserved local
administrator password. RouteNest stores only an Argon2id verifier, rotates the code
after use, and revokes administrator sessions. It is distinct from a client API key,
Provider Credential, bootstrap capability and offline recovery secret.

**Desktop Setup Capability**:
An in-memory first-run capability bound to the trusted Wails setup origin. It authorizes
creation of the administrator only after fresh-install evidence is rechecked. It is not
an HTTP credential and becomes unusable after setup completes or requires restart.

**Trusted Desktop Device**:
An explicitly authorized desktop installation that may sign in without re-entering the
administrator password until its trust expires, is paused, or is revoked. Its device
credential is distinct from an administrator password, browser login, recovery credential,
or proof of application identity.

**Logical Model**:
The client-facing model identity requested from RouteNest. A Logical Model may be
available through more than one Model Offering.

**Model Offering**:
One Provider Instance's ability to serve one Logical Model. Its identity is the pair of
stable Provider Instance ID and raw model ID; access class, upstream identity, limits,
availability, quota, cooldown and catalog status belong to the offering. Mutable
custom-provider offering state is scoped by Provider Instance ID and raw model ID; one
provider's classification or status does not apply to a sibling offering with the same
model ID.

**Model Source**:
The compatibility origin of a model record, such as Cline remote discovery, OpenCode
Zen discovery or a custom Provider Instance. A Model Source is not a substitute for a
stable Provider Instance ID.

**Default Model**:
The global client model reference RouteNest uses when a request does not select one.
The current saved setting is a bare catalog Logical Model ID. Provider-qualified
references are a proposed extension, not a generally accepted current default setting.

**Routing Alias**:
A client-visible model name mapped to another model reference, optionally pinned to a
Provider Instance. It is routing configuration, not a Model Offering.

**Combo Reference**:
One ordered entry in a named routing combo. It identifies a model reference and may
retain a Model Source and Provider Instance pin. Legacy display-name pins can exist,
but they are not canonical mutation identity.

**Availability Observation**:
A fact learned from an upstream response, catalog, configured budget or health probe,
such as rate limiting, quota exhaustion, expired authentication, model removal,
transient failure or recovery.

**Cooldown**:
A temporary period in which a Provider Instance, Provider Credential, Model Offering
or egress endpoint is ineligible. A cooldown deadline does not by itself prove an
upstream quota reset time.

**Quota Reset**:
A confirmed or inferred time when a quota window becomes available again. It has
evidence and confidence; unknown reset information remains unknown.

**Egress Profile**:
A named outbound-network policy containing proxy endpoints and selection behavior.
The term does not imply that generic profile creation or binding is available in the
current administrator UI.

**Free-first Routing**:
Candidate selection that prefers eligible free Model Offerings across providers.
Unknown or unverified access never qualifies as free.
