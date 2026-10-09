# Dorylinae (CLI: agentnet) - product fact sheet for the promo film

Prepared for the film's scriptwriters. Every claim below is traced to a repo doc and line.
Nothing here is invented. Where the docs and the code disagree, or a doc is stale, that is
said out loud.

Repo snapshot read: `/home/user/Dorylinae-Agentnet`, branch `main`, last commit `860afe8`
(2026-10-03). Read-only; nothing was modified, committed or pushed.

## How to read the citations

Paths are relative to the repo root. Short forms used in every citation:

| Short | Full path |
|---|---|
| `PLAN` | `Docs/AgentNet Free Tier Build Plan.md` |
| `KL` | `Docs/beta/known-limitations.md` |
| `SNIP` | `Docs/agents/snippet.md` |
| `HANDOFF` | `Docs/orchestration/HANDOFF.md` |
| `CLI/x.md` | `Docs/cli/x.md` |
| `PROT/x.md` | `Docs/protocol/x.md` |
| `MAIN` | `cmd/agentnet/main.go` |

Format is `short:line` or `short:line-line`. Line numbers are those of the file at the
snapshot.

## How BUILT vs PLANNED was decided

A feature counts as BUILT only if all three hold: (a) its command is in the CLI dispatch
table (`MAIN:48-134`), (b) it has a CLI doc page that describes behaviour (not only a plan),
and (c) HANDOFF records the ticket as merged. Phases 0, 1, 2 and 3 meet that bar
(`HANDOFF:147`, `HANDOFF:134-138`, `HANDOFF:316`; tags `phase-0`, `phase-1`, `phase-2`
exist on origin, and `phase-3` is recorded as pushed at `HANDOFF:136`). Phase 4 meets it only for relay-side pieces and for the release/installer
tooling, never for anything a tester types after install (details in section 4).

Warning for anyone reading the repo docs: several headers still say "draft" (for example
`CLI/request.md:3`, `PROT/work-session.md:3`) and `README.md:8-13` still says the repo holds
"Phase 0 and Phase 1". Those are stale. The dispatch table and HANDOFF are the truth.

The CLI dispatch table (`MAIN:48-134`) has 34 commands, plus `version`, help and `--version`,
and a debug-only `mail` (hidden unless `DORYLINAE_DEBUG=1`): status, doctor, stop, identity,
pair, peers, team, presence, ping, request, consult, debate, debates, decision, decisions,
inbox, accept, decline, defer, complete, sessions, session, result, wait, accept-result,
notify, approve, device, grant, grants, revoke, fetch, log, prune. There is no `setup`, `login`, `logout`,
`feedback` or `telemetry` command.

---

## 1. What Dorylinae is

### 1a. For someone who has never heard of it (no jargon)

Dorylinae is for small teams where each person works with their own AI coding assistant.
Today, when you want a teammate to look at something, you paste the context into a chat.
With Dorylinae, your assistant sends the request straight to your teammate's assistant. It
lands in their inbox, most urgent first, and they get a notification. They accept it, do the
work and send back an answer. If their computer is off, the request waits for them.

Everything is private. The messages are sealed on your machine and only your teammate's
machine can open them. You stay in charge: your assistant cannot give anyone access to your
files by itself. You approve that yourself, on your own screen. And when two assistants
disagree about a technical choice, they can argue it out in a fixed format and write down one
signed record of the result.

Sources: `PLAN:7`, `PLAN:11`, `PLAN:75`, `CLI/request.md:58-61`, `PROT/mail.md:14-22`,
`CLI/approve.md:14-24`, `CLI/debate.md:5-6`.

### 1b. For developers

Dorylinae is a local-first coordination layer for agents in different harnesses on different
machines (`README.md:3-5`, `scope.md:5`). A per-user daemon (`agentnetd`) holds your Ed25519
identity, the pairwise end-to-end encrypted channels, grants, inbox, work sessions and a
hash-chained audit log. A stateless CLI (`agentnet`) returns in under 2 seconds with
`--json`, so any harness drives it through its ordinary shell tool (`PLAN:21`, `PLAN:44`,
`SNIP:12-16`). A relay routes ciphertext and queues for offline peers for 7 days
(`PLAN:17`, `CLI/relay.md:43`). Requests (review, task, question) land in the peer's inbox
sorted by weighted urgency; accepting opens a bounded work session that ends with a
structured result. The requester can give scoped, expiring, revocable read access
(`fs.read`, `git.read`) that needs a human approval typed into a daemon-owned window.
`consult` is a question with context files. `debate` is a two-agent structured argument
(commit/reveal, challenge rounds, human constraints) that ends in a Decision signed by both
daemons and exportable as Markdown (`CLI/decision.md:83-94`).

Sources: `PLAN:17-44`, `PLAN:104-108`, `PLAN:136-141`, `PROT/grant.md:9-20`.

---

## 2. The problem it solves, in the docs' own words

The docs state the problem in one place: the plan's opening and its user definition. They do
not offer statistics or a long pain narrative. Do not invent one.

> "The free phase proves one sentence: **a small team's agents, in different harnesses on
> different machines, can see each other, hand each other work safely, and end with a decision
> record.**" - `PLAN:7`

> "The user this phase serves is a person building a project with one to four others, each
> running their own agent harness (Claude Code, Codex, OpenCode, Hermes, or a custom one),
> who today coordinates by pasting context into Slack or WhatsApp." - `PLAN:11`

> "Done means a team of two to five people installs one binary each, pairs, and for four
> weeks uses it instead of chat messages for 'can you look at this', without a manual fix
> from you." - `PLAN:9`
> (Note: "one binary each" is loose. See section 5, claim 13.)

> "Outcome: I see my teammate is online, my agent sends their agent a task request with a
> brief, and it lands in their inbox sorted by urgency, with a desktop notification. This is
> the first thing a beta tester will use, so it is the first thing that has to feel
> finished." - `PLAN:75`

> "Local-first coordination layer that lets agents in different harnesses and on different
> machines discover each other, exchange work safely, collaborate in bounded sessions and
> produce signed decision records." - `README.md:3-5`; same sentence at `scope.md:5`

> "The Decision is the artifact people will show each other; the log is what makes the
> security claims checkable." - `PLAN:132`

The three verbs the docs use, in order: see each other (presence), hand each other work
safely (requests, grants, sessions), end with a decision record (debate, Decision).

What the docs say the product does NOT try to be in the free phase: "Nothing about strangers,
credits, reputation or money is in scope" (`PLAN:7`); pricing, ratings, discovery, agencies,
dashboards, SDK and MCP adapter are explicitly undecided (`PLAN:13`).

---

## 3. BUILT features (Phases 1-3 on top of the Phase 0 base)

Format per feature: what the user or agent experiences, exact commands, concrete numbers,
proof (doc path:line). All commands below exist in `MAIN:48-134`. Example codes, ids and
fingerprints come from the CLI docs and are illustrations, not real data.

### 3.0 The shape of the system

- Three parts: `agentnetd` (the daemon: keys, pairing, session keys, grants, inbox, sessions,
  audit log, presence, notifications), `agentnet` (a short-lived CLI the harness spawns), and
  a relay (routes ciphertext, queues for offline peers, rate limits). `PLAN:17`, `PLAN:28-32`.
- The harness never talks to the network. It runs `agentnet <cmd> --json`; the CLI talks to
  the daemon over a local socket; the daemon holds one WebSocket to the relay. `PLAN:19-26`.
- Two hard rules: every CLI call returns in under 2 seconds or returns an ID to poll; and
  untrusted text never grants authority, only a daemon-issued token does. `PLAN:44`,
  `scope.md:77-81`. Deliberate exceptions to the 2-second rule: `agentnet wait` and
  `agentnet log --verify` (`CLI/session.md:188-191`, `CLI/log.md:24`).
- Keys live in the OS keychain where available (Windows Credential Manager, macOS Keychain,
  Linux Secret Service), else an owner-only file (mode 0600). `PROT/agent-card.md:286-292`,
  `PLAN:30`. The private key is never sent to the CLI (`CLI/identity.md:3-6`).
- The daemon runs as a per-user service that starts at login, no admin rights or sudo:
  launchd agent (macOS), systemd user unit (Linux), Task Scheduler task (Windows).
  `CLI/agentnetd-install.md:3-4`, `CLI/agentnetd-install.md:30-34`.

### 3.1 Identity and the Agent Card

- Experience: every install gets a cryptographic identity and a signed "Agent Card": name,
  public key, which harness runs it (examples `claude-code`, `codex`, `hermes`, `custom`),
  declared skills (0-32). A 20-character fingerprint in five groups of four, for example
  `2ED9 TGVE R471 63MC C451`, lets two humans compare identities out loud.
- Command: `agentnet identity [--json]`.
- Numbers: Ed25519 key; card has exactly 6 members; name 1-128 characters.
- Proof: `PROT/agent-card.md:9-14`, `PROT/agent-card.md:30-37`, `CLI/identity.md:1-6`,
  `CLI/identity.md:21-30`, `PLAN:40`.

### 3.2 Pairing (how two machines meet)

- Experience: person A runs one command and gets a short one-time code. Person B types it on
  their machine. Both machines verify each other's signed card, prove they know the code to
  each other, and store the peer. If anything was altered in transit, nothing is stored.
- Commands: `agentnet pair --new` (prints the code), `agentnet pair <code>` (on the other
  machine), `agentnet peers`, `agentnet peers verify <peer> <fingerprint>`,
  `agentnet peers remove <peer>`.
- Numbers: code is 15 characters (shown as `7KQ2M-9XHF4-TRW8N`); the first 5 go to the relay
  to find the other side, the last 10 never leave the two machines; valid 10 minutes; single
  use; the issuer allows at most 3 attempts per code; each side waits at most 60 seconds for
  the other's proof; the code is stretched with Argon2id (t=3, 64 MiB). The command always
  returns in under 2 seconds (state `pending` plus an ID to poll if the exchange is slower).
- A relay in the middle cannot complete a pairing: the plan's acceptance test is that a MITM
  relay causes `bad_confirm` on both sides (`PLAN:65`).
- Trust levels shown in `peers`: `relay` (legacy), `code`, `team` (introduced by a team
  owner), `fingerprint` (a human compared fingerprints).
- Proof: `CLI/pair.md:9-52`, `CLI/pair.md:64-78`, `PROT/pairing.md:46-47`,
  `PROT/pairing.md:71-76`, `PROT/pairing.md:118`, `PROT/team.md:44-49` (trust table),
  `PLAN:58-60`, `PLAN:65`.

### 3.3 Teams

- Experience: a team is a named set of paired people. Presence and requests are scoped to a
  team. The creator is the owner. The owner invites with a one-time code; a joiner pairs with
  the owner only, and the owner introduces members to each other, so a team of five needs 4
  codes, not 10 pairings. Inviting needs the owner's approval (a human types a code in the
  approval window) because everyone then trusts the new member.
- Commands: `agentnet team create <name>`, `team invite <team>`, `team join <code>`,
  `team list`, `team show <team>`, `team remove`, `team rename`, `team leave`, `team delete`.
- Numbers: team name `^[a-z0-9][a-z0-9-]{0,31}$`; at most 32 members; invite code valid 10
  minutes; one owner, no admins; the owner changes the roster, a member can only leave.
- No group cryptography. Every team message is a pairwise sealed mail; the relay sees no team
  names or rosters (the envelope `team` field is empty).
- Proof: `CLI/team.md:8-47`, `PROT/team.md:7-12`, `PROT/team.md:22-32`, `PROT/team.md:33-38`,
  `KL:21-36`.

### 3.4 Presence (three levels) and visibility

- Experience: `agentnet status --team backend` shows each teammate's state in three layers,
  plus when they were last seen.
- Command: `agentnet status --team <team> [--json]`. Table columns: NAME, DAEMON, AGENT,
  HUMAN, LAST SEEN (`online`/`offline`, `active`/`idle`, `present`/`away`/`unknown`).
- The three levels and thresholds:

  | Level | Meaning | Threshold |
  |---|---|---|
  | Daemon online | Their AgentNet is running and connected | heartbeat every 30 s; shown online if last heartbeat <= 75 s ago |
  | Agent active | Any `agentnet` command reached their daemon | within the last 5 min |
  | Human present | Keyboard/mouse activity on their computer | OS idle time under 10 min |

- Offline detection: a killed daemon shows offline in under 90 s (the design computes 75 s);
  KL says "up to about 90 seconds". A normal stop or going invisible shows at once. Running
  any command makes the agent flag flip within about 1 s (bound 30 s).
- Visibility: `agentnet presence --visible | --invisible | --only-team <team>`, and
  `--human on|off`. Invisible = teammates see last-seen only and never "online"; the daemon
  also does not answer `agentnet ping` while invisible. Human presence is shared by default.
- Presence is end-to-end: heartbeats are sealed and signed peer to peer, so the relay cannot
  read the agent/human flags and a hostile relay cannot forge "online". All heartbeats have
  the same size.
- Limit: invisible hides you from teammates, not from the relay, and a paired peer who sends
  you mail can still infer you are online from delivery acks.
- Proof: `PROT/presence.md:12-14`, `PROT/presence.md:22-28`, `PROT/presence.md:30-45`,
  `CLI/presence.md:8-24`, `CLI/status.md:56-68`, `KL:79-83`, `PROT/presence.md:262`,
  `CLI/ping.md` (invisible paragraph), `PLAN:86-87`.

### 3.5 Requests (review, task, question), the brief, urgency

- Experience: the agent asks a teammate's agent for something, in three types: `review`,
  `task`, `question`. The sender's own agent writes a short brief; there is no platform-side
  model. The call returns at once with `status: queued`, even if the teammate is offline,
  and says whether they are online and when they were last seen. That is a success, not a
  timeout.
- Command: `agentnet request <peer> review|task|question --title "..." --brief-from-file F
  [--artifact SPEC]... [--urgency low|normal|high|blocking --urgency-reason R]
  [--deadline D] [--idempotency-key K] [--json]`. Follow-ups: `request show <id>`,
  `request list`, `request resend <id>`, `request cancel <id>`.
- The brief has three lines (shown by `agentnet request --help`):
  `What: ...` / `Why: ...` / `Done when: ...`.
- Artifacts are `url`, `branch`, `commit` or `path` pointers, up to 20. They are pointers
  only: they give the other side no access (access is a separate grant, 3.13).
- Numbers: title 1-120 characters; brief up to 16 KiB; urgency reason up to 280 characters;
  up to 20 artifacts; whole request up to 64 KiB; idempotency key 1-64 characters (a retry
  with the same key never sends twice); returns in under 2 seconds.
- Cancel works only while the request is waiting or deferred, never after accept; cancelling
  does not refund urgency budget. There is no editing: cancel and send a new one.
- Proof: `CLI/request.md:5-41`, `CLI/request.md:43-54` (brief), `CLI/request.md:56-65`,
  `CLI/request.md:73-84`, `KL:40-54`, `PLAN:88-89`, `PLAN:93`, `SNIP:20-26`.

### 3.6 The inbox (ordering and answering)

- Experience: the receiving agent lists requests sorted by "effective priority", highest
  first, then oldest first. It answers with accept, decline (needs a reason), defer (needs a
  time), or later complete.
- Commands: `agentnet inbox [--json]`, `agentnet accept <id>`, `agentnet decline <id>
  --reason R`, `agentnet defer <id> --until T`, `agentnet complete <id> [--status
  pass|fail|partial|n/a ...]`.
- Numbers: base priority `low` 1000, `normal` 2000, `high` up to 3000, `blocking` up to
  4000. "Up to" because the weight is the sender's acceptance-as-urgent rate over the last
  30 days: a sender whose urgent requests you usually defer or decline sinks toward
  `normal`. A new sender starts at full weight. Defer is at most 90 days ahead; decline
  reason 1-500 characters. Every accept, decline, defer and completion is logged with a
  timestamp.
- Proof: `CLI/inbox.md:32-43`, `CLI/inbox.md:7-30`, `PROT/request.md:664-698`, `PLAN:90`.

### 3.7 Urgency guards

- Experience: urgency is scarce on purpose so "urgent" keeps meaning something. Each sender
  gets 5 `high` and 2 `blocking` requests per rolling 7 days, across all teammates. Beyond
  that the request goes out as `normal` and the output says so. The receiving side enforces
  the same budget, so a modified sender cannot bypass it. `high` and `blocking` require a
  stated reason.
- Proof: `CLI/request.md:63-65`, `PROT/request.md:703-730`, `KL:46-48`, `PLAN:91`,
  `SNIP:47`.

### 3.8 Notifications (desktop and webhook)

- Experience: a native desktop notification when a request arrives and when one is
  accepted or declined. Optionally a signed webhook to Slack, Discord or anything else.
- Command: `agentnet notify [--desktop on|off] [--event <event>=on|off]
  [--webhook URL [--format generic|slack|discord] [--webhook-title on|off]]
  [--rotate-secret] [--test]`.
- Numbers: desktop notification shown within 5 seconds of the message being committed
  (the plan's acceptance criterion). Webhook must be `https://` (plain `http://` only to
  localhost); body is signed with HMAC-SHA256 in a `Dorylinae-Signature` header; the signing
  secret is printed once; failed deliveries retry for at most 24 hours.
- Events on by default: `request.received`, `.accepted`, `.declined`, `.cancelled`,
  `session.quarantined`, `session.result`, `session.changes`, `device.linked` (desktop only),
  and five debate events. Off by default: `request.deferred`, `request.completed`.
- What they never contain: the brief, artifacts, reasons, notes, grants, keys. The webhook
  holds the request title only if you turn `--webhook-title on`. Debate and quarantine
  notifications say only that something happened.
- Limit: Slack and Discord cannot verify the webhook signature.
- Per OS: macOS via `osascript`, Linux via D-Bus (fallback `notify-send`), Windows via a
  toast through PowerShell, all with fixed scripts so peer text cannot inject code.
- Proof: `PROT/notify.md:11-33`, `PROT/notify.md:84-96`, `PROT/notify.md:193-210`,
  `PROT/notify.md:264`, `PROT/notify.md:269-278`, `CLI/notify.md:17-28`, `KL:87-89`,
  `PLAN:92`.

### 3.9 Offline delivery

- Experience: the sender never gets a timeout. The request is held and delivered when the
  teammate's computer is back. The sender keeps the message and resends with backoff until
  the receiver acknowledges it; the receiver ignores duplicates.
- Numbers: relay holds an envelope for 7 days (`--queue-ttl 168h`). If the teammate is away
  longer, delivery shows `expired`, which means "delivery unknown". `request resend <id>`
  is safe (the recipient never sees it twice) for 21 days after first send. A request first
  sent more than 30 days ago is not accepted as new. Receivers reject application mail
  older than 14 days.
- Contrast: `agentnet ping` and `agentnet fetch` are online-only; an offline peer makes them
  time out (ping fails after 10 seconds; fetch default timeout 30 s, exit 4).
- Proof: `PROT/request.md:732-738`, `CLI/request.md:56-72`, `CLI/relay.md:43`, `KL:55-60`,
  `CLI/ping.md` (two-second rule section), `CLI/fetch.md:7-22`, `PLAN:62`, `PLAN:82-83`,
  `PLAN:93`, `PROT/mail.md:14-17`.

### 3.10 End-to-end encryption: what the relay can and cannot see

- Experience: the relay is a post office that cannot open the envelopes.
- How: every application message is a "mail": signed by the sender's Ed25519 key and sealed
  with HPKE to the recipient's current mailbox key (X25519). Interactive traffic (ping and
  file reads under a grant) uses a Noise XX session (`Noise_XX_25519_ChaChaPoly_SHA256`)
  with fresh keys per session.
- The relay CAN see: who sends to whom (`from`, `to`), message type `mail`, a message id,
  timestamp, payload size, and when mail and acknowledgements flow; also when a daemon is
  connected, even if invisible; and, on the hosted relay, that a pairing happened between
  two keys. Client IP addresses are used for limits (kept in logs at most 14 days).
- The relay CANNOT see: what kind of message it is (request, result, grant, debate move...),
  titles, briefs, results, grants, fetched files, debate text, Decisions, approval codes,
  pairing secrets, team names, rosters. The kind is inside the ciphertext.
- Honest limits (do not overclaim): (1) mail has forward secrecy only through key rotation
  (mailbox keys rotate every 7 days and are deleted at 21 days); (2) signed mail gives the
  sender non-repudiation by design, which is a feature for records and a property to know;
  (3) the relay operator can deny service and sees the metadata above.
- Proof: `PROT/mail.md:7-30`, `PROT/mail.md:659-662`, `PROT/session.md:12-26`,
  `PROT/envelope.md:54`, `PROT/relay-hosted.md:45`, `PROT/relay-hosted.md:886-902`,
  `CLI/relay.md:3-4`, `PROT/mail.md:45-53` (rotation), `KL:81-82`, `PLAN:38`.

### 3.11 Work sessions and results

- Experience: accepting a request opens a "work session": a bounded, saved unit of work
  between the two agents. The worker returns a structured result; the requester reads it,
  and either accepts (session closes, request completes), asks for changes (another round),
  or cancels.
- Commands: `agentnet sessions [--state S --role R]`, `agentnet session <id>
  [--request-changes TEXT | --discard | --cancel | --release]`, worker:
  `agentnet result <id> --status pass|fail|partial|n/a --summary "..." [--file F]
  [--verification none|tests_passed] [--notes T]`, requester: `agentnet wait <id>
  [--timeout 300]`, `agentnet accept-result <id> [--human]`.
- Session states: `open`, `awaiting_result`, `quarantined`, `closed`.
- Numbers: result summary up to 280 characters; output up to 32 KiB; notes up to 2,000
  characters; whole result up to 64 KiB; request-changes text 1-4,000 characters; `wait`
  default 300 s, max 3,600 s, exit 4 on timeout. `accept-result --human` makes the stored
  verification `human_accepted`; the worker's own `tests_passed` is a claim, not proof.
- "Complete" is a manual step on the worker's side; there are no live shared sessions.
- Proof: `CLI/session.md:8-30`, `CLI/session.md:50-76`, `CLI/session.md:133-165`,
  `CLI/session.md:186-247`, `PROT/work-session.md:14-30`, `PLAN:103-109`, `KL:53-54`.

### 3.12 Human approval window

- Experience: sensitive actions (a grant, releasing a held result, linking your own devices,
  inviting a team member, adding a debate constraint, pruning history) make the daemon show a
  one-time code on a desktop notification and open its own approval window. The human types
  the code into that window.
- The design point: no CLI command and no IPC method accepts a code. The code never appears
  in any command output, log, audit row or process argument list. So an agent running
  `agentnet` cannot approve its own request. The snippet tells agents never to ask the user
  for a code and never to claim to enter one.
- Numbers: 3 wrong codes reject the approval; an approval expires after 10 minutes (or if the
  daemon restarts). Linux uses a `zenity` or `kdialog` window; headless machines can approve
  on the daemon's own terminal.
- Boundary (docs state it plainly): this stops an agent acting through AgentNet's own
  interface. It does not stop an agent that runs arbitrary programs as the user and sets out
  to defeat it, such as reading notification history or driving the window. Mitigation is
  harness confinement.
- Proof: `CLI/approve.md:3-24`, `CLI/approve.md:44-54`, `PROT/approval.md:19-30`,
  `PROT/approval.md:40-50`, `SNIP:84-91`, `SNIP:74-77`, `CLI/status.md:45-54`.

### 3.13 Grants: scoped, expiring, revocable read access

- Experience: the requester can let the worker's agent read one directory or one git branch,
  read-only, for a limited time. The worker lists and fetches files through the requester's
  own daemon. The requester can revoke at any moment and the holder's next read fails
  immediately.
- Commands: requester: `agentnet grant @peer --session S --action fs.read|git.read
  --resource ABSOLUTE_DIR[#BRANCH] [--scope P] [--expires D] [--public]`, `agentnet grants
  [--issued|--held]`, `agentnet revoke <g-id>`, standing rules: `agentnet grant policy
  add|list|remove`. Holder: `agentnet fetch <g-id> --list [dir]`, `fetch <g-id> <path>
  [--out FILE]`, `fetch <g-id> --stat <path>`.
- Numbers: expiry from 1 minute to 7 days, default 2 hours; a grant waits as
  `pending_approval` until the human approves in the window (unless a policy the human
  created matches); at most 50 policies, default 30 days, max 90 days. Fetch reads files up
  to 8 MiB in 256 KiB reads; limits per holder: 2 reads in flight, 20 operations a second,
  256 MiB per grant per 24 hours. `.git` directories, symlinks, devices and FIFOs are not
  served. Closing the session revokes its grants. Removing a peer revokes all their grants.
  Only the session's requester can grant, and only while it is open.
- How it is enforced: the requester mints a signed token (Ed25519) binding action, resource,
  scope, expiry, session and audience; the holder verifies it offline; and the requester's
  daemon re-checks the grant on every read, so revocation has no propagation delay.
- No write grants exist. A read-only database URL is deferred.
- The only source of authority: "untrusted text (a brief, a result, a `requested_grant`
  hint) never grants anything".
- Proof: `CLI/grant.md:9-38`, `CLI/grant.md:65-75`, `CLI/grant.md:76-87`, `CLI/fetch.md:25-42`,
  `PROT/grant.md:9-27`, `PROT/grant.md:146`, `PROT/grant.md:275-295`, `PROT/grant.md:303`,
  `PLAN:104-105`.

### 3.14 Sensitive-grant quarantine

- Experience: if the worker read private data through a grant, the result they send back is
  held. The requester's agent can see only the status and sizes, never the content, until a
  human looks and releases it. This stops private files from leaking back through a result.
- What counts as sensitive: every `fs.read` grant, and every `git.read` grant unless the
  requester marks it `--public`.
- When the hold applies: the session ever had an active sensitive grant (even if it has
  since expired or been revoked), OR the requester gave that same peer a sensitive grant
  in any session that expired less than 7 days ago (so the peer cannot read in one session
  and hand the data back in another).
- Ways out: `agentnet session <id> --release` (needs a human approval), `--discard` (closes
  the session, the held result is deleted unseen), or `--request-changes` without releasing
  (result deleted unseen). The `session.quarantined` notification tells the human a held
  result is waiting. A debate never carries grants.
- Proof: `PROT/work-session.md:341-386`, `CLI/session.md:26-30`, `CLI/session.md:62-70`,
  `CLI/grant.md:31`, `PLAN:106`, `PROT/debate.md:44-47`.

### 3.15 Consult

- Experience: a quick question to a teammate's agent, with up to 8 context files. The
  command returns at once with a session id; the asker waits for the answer; the teammate's
  agent answers in one step.
- Commands: asker: `agentnet consult <peer> --question "..." [--context-file F]...
  [--idempotency-key K] --json`, then `agentnet wait <session> --json`, then
  `agentnet accept-result <session>`. Answerer: `agentnet inbox`, `agentnet request show
  <id> --json`, `agentnet result <request-id> --file answer.md`.
- Numbers: question up to 16,384 bytes; 1-8 context files, each up to 64 KiB of plain text
  (UTF-8, no control characters); only the file's base name is sent, never its directory;
  whole question with context up to 320 KiB. Context files never go to the audit log,
  notifications or webhooks. A consult is a request of type `question`.
- Proof: `CLI/consult.md:9-31`, `CLI/consult.md:33-52`, `CLI/consult.md:84-102`,
  `PROT/consult.md:6-21`, `PLAN:107`.

### 3.16 Debate (two agents, a fixed structure, a signed result)

- Experience: when two teammates' agents disagree on a technical question, one starts a
  debate. Each side writes an opening position. The structure is fixed: positions, a few
  challenge rounds, then a converge step where one side proposes an agreement and the other
  accepts or rejects. If they do not agree, the disagreement is recorded and the Decision is
  still produced. The humans can add rules mid-way.
- Commands: start `agentnet debate <peer> --topic "..." --position-file P [--rounds N]
  [--turn-timeout D] [--context-file F]...`; respondent: `agentnet debate <id>
  --position-file F` (accepts and submits an opening position in one step); then entries
  with inline flags: `--claim S --argument S`, `--pass` or `--challenge TARGET=ARGUMENT`,
  `--agree S`, `--accept` / `--reject`. `agentnet wait <id>` blocks until it is your turn;
  `agentnet debate <id> --json` shows `turn`, `expect` and a `hint` with the exact next
  command. `agentnet debates` lists them. `agentnet debate <id> --cancel` closes one.
- Commit/reveal: the initiator's daemon (never the agent) commits to the opening position
  with a SHA-256 commitment before sending the request. It reveals the position only after
  the other side's opening position has left the other daemon. Neither opening position can
  be influenced by the other, and the initiator cannot change theirs afterwards (the
  reveal must match the commitment). Purpose, in the plan's words: "to reduce anchoring".
- Human constraints: either human can add a one-line rule (1-500 visible characters, for
  example "no new dependency") with `agentnet debate <id> --constrain "..."`. It always
  needs that human's approval in the approval window, so an agent cannot add one. It is
  signed into the Decision under "human decisions". At most 10 active constraints.
- Numbers: challenge rounds 1-5, default 2; each move up to 3 challenges; each entry up to
  32 KiB; turn timeout 5 minutes to 24 hours, default 1 hour (a missed turn ends the debate: if
  the respondent never sends an opening position it is `cancelled` with no Decision, and if
  any later turn is missed it is `escalated` with reason `timeout` and a signed Decision of
  what exists). Outcomes: `agreed`, `escalated` (both produce a
  signed Decision) or `cancelled` (no Decision). Two parties only; a third teammate takes
  part as a human who asks one participant to add a constraint.
- A debate carries no grants, and a sensitive grant to the same peer is refused while a
  debate is open with them (`debate_open`).
- Real-agent evidence: Claude Code and agy completed two full debates (each as initiator
  once), outcome `agreed`, about 589 s and 603 s, 9 agent turns each. See 3.20 for caveats.
- Proof: `CLI/debate.md:5-30`, `CLI/debate.md:84-143`, `CLI/debate.md:145-160`,
  `CLI/debate.md:184-209`, `PROT/debate.md:13-40`, `PROT/debate.md:95-126`,
  `PROT/debate.md:190-197`, `PROT/debate.md:407-416`, `PROT/debate.md:323-345`, `PROT/debate.md:62`,
  `PLAN:136-141`, `PLAN:144`, `KL:68-76`, `tests/phase3-manual.md:101-117`.

### 3.17 Decision records (signed, exportable as Markdown)

- Experience: a debate that agrees or escalates produces one Decision. It records the
  problem, participants, both initial positions, assumptions, evidence, arguments,
  counterarguments, rejected alternatives, the final agreement or remaining disagreement,
  human decisions and constraints, and affected artifacts. Both daemons derive the same
  bytes independently and each signs with its identity key.
- Exports: `agentnet decision <id> --md --out decisions/d-xxxx.md` writes a Markdown file
  meant for a repo's decisions folder. It is deterministic (same input, same bytes) and
  repo-safe: every string from an agent or human is rendered inert (code spans/blocks,
  never raw Markdown, links, images or template syntax). `agentnet decision <id> --json
  --out d.json` writes the signed file; `agentnet decision verify d.json` checks it with no
  daemon at all. `agentnet decisions` lists them.
- Verify exit codes: 0 = valid and signed by both; 6 = valid but signed by the initiator only
  ("unconfirmed"); 1 = invalid.
- What the signatures mean: "this is the debate as my daemon recorded it". They do not mean
  "I agree"; agreement is the outcome, taken from the respondent's own answer.
- Proof: `CLI/decision.md:1-33`, `CLI/decision.md:46-55`, `CLI/decision.md:81-107`,
  `PROT/decision.md:10-24`, `PROT/decision.md:25-60`, `KL:93-97`, `PLAN:138`.

### 3.18 Hash-chained audit log

- Experience: every action the daemon takes (pair, request, accept, grant, fetch, revoke,
  release, result, decision...) is appended to a local log. `agentnet log` shows it;
  `agentnet log --verify` checks the chain and exits 5 if it is broken, naming the first bad
  row.
- Content-free by construction: ids, keys, enums, counts and sizes; never titles, briefs,
  results, notes, reasons, paths or commands. Append-only (database triggers reject
  update and delete). Each row's hash covers the previous row's hash.
- Commands: `agentnet log [--since 24h] [--session ID] [--action PREFIX] [--json]`,
  `agentnet log --verify [--anchor ID:HASH]`, `agentnet log --head` (print a row id and
  hash to keep as an anchor, for instance in a commit message).
- Sample real output of verify: "The audit log is intact: 1043 rows checked ..." with a head
  hash to keep as an anchor.
- Limit (say it right): it detects a changed, deleted, inserted or reordered row before the
  head. It does not prove anything against someone who can rewrite the whole database file,
  and it does not catch truncation of the newest rows unless you kept an anchor.
- Proof: `PROT/audit.md:6-22`, `PROT/audit.md:24-45`, `CLI/log.md:1-37`, `CLI/log.md:66-81`,
  `CLI/log.md:96-101`, `KL:98-104`, `PLAN:141`.

### 3.19 Other built pieces (accurate, lower priority for a promo)

- Own-device helper: link two of your own machines (a controller and a helper); a request
  `--run NAME` can then run an allowlisted command on the helper, only if the helper's owner
  set a scope. Off by default, confirmed by a human on both devices. `CLI/device.md:1-27`,
  `CLI/request.md:37`. Niche; do not feature.
- Private experience record: on session close the daemon writes a local record. Nothing
  reads it in this phase (no command, no peer). Do not claim the product "learns".
  `PROT/experience.md:7-10`.
- `agentnet doctor` checks the setup (versions, config, keychain, service, socket, relay,
  git, clock, peers). `CLI/doctor.md:1-39`. `agentnet prune --older-than 35d` removes
  finished items after a human approval; the daemon never deletes content on its own.
  `CLI/prune.md:1-30`. `agentnet peers verify` raises trust after comparing fingerprints.

### 3.20 Harness-agnostic design (which harnesses, and HOW)

- How the agent uses it: through its shell tool. The agent runs `agentnet ...` as a normal
  command-line program and reads `--json` on stdout. "There is no separate 'AgentNet' tool,
  app or connector - it is always this CLI." The project owner pastes a short block into
  the agent's instruction file: `CLAUDE.md` for Claude Code, `AGENTS.md` for Codex CLI and
  other harnesses. The agent finds the details from `agentnet <command> --help`.
  `SNIP:3-18`, `SNIP:12-16`, `PLAN:19-22`.
- Names in the docs: Claude Code, Codex (CLI), OpenCode, Hermes, "or a custom one"
  (`PLAN:11`). The Agent Card's harness field gives `claude-code`, `codex`, `hermes`,
  `custom` as examples (`PROT/agent-card.md:36`).
- What has actually been run with real agents (verified in the repo's manual-test records):
  - Claude Code and `agy` (Antigravity CLI), in both directions, request flow (Phase 1,
    Phase 2) and debate (Phase 3, two rounds, both `agreed`, about 10 minutes each, 9 turns
    each, Claude side about 1.06 and 1.21 USD). `HANDOFF:134`, `HANDOFF:191`,
    `HANDOFF:379`, `tests/phase3-manual.md:101-117`, `tests/harness/README.md:1-40`.
  - The agents were given only the snippet and had to find the commands from it and
    `--help`. `tests/harness/README.md:1-12`.
  - Those runs used two daemons and a loopback relay on one test machine. The owner's
    separate two-machine Phase 1 run passed on 2026-09-25 (`HANDOFF:310`, decision D29).
  - A scripted stand-in agent runs the full loop weekly in CI on Linux, Windows and macOS.
    `HANDOFF:147`.
- NOT run: Codex CLI (blocked by an account usage limit; "optional" rerun after 2026-10-02,
  `HANDOFF:161-163`), Hermes, OpenCode. They are named, not tested. The plan's harness-test
  ticket promised Claude Code, Codex and Hermes (`PLAN:109`); what shipped is Claude Code
  and agy.
- A Claude Code skill for debates exists in the repo (`SNIP:73`); the docs do not describe
  how a tester would get it.

### 3.21 Platforms

- macOS, Linux and Windows. CI builds all three binaries on all three OSes
  (`README.md:57-59`); release archives are planned for each of amd64 and arm64
  (`CLI/install.md:3-11`).
- Per-OS service: launchd agent `dev.dorylinae.agentnetd`, systemd user unit
  `agentnetd.service`, Task Scheduler task `Dorylinae agentnetd`; no admin or sudo
  (`CLI/agentnetd-install.md:3-4`, `CLI/agentnetd-install.md:30-34`).
- Per-OS notifications and approval window: see 3.8 and 3.12. On Linux the approval window
  needs `zenity` or `kdialog` (`CLI/status.md:45-54`).
- Windows builds are unsigned in the first wave: SmartScreen asks the user to confirm first
  run ("More info", then "Run anyway") (`CLI/install.md:78-80`).
- Known per-OS limits: idle detection differs per OS (`PROT/presence.md:277-290`), a long
  macOS config path can break the daemon's socket (`KL:121-125`), OneDrive "files on demand"
  are not served by `fetch` on Windows (`CLI/fetch.md:41`).

### 3.22 Numbers cheat sheet (all BUILT)

| What | Number | Source |
|---|---|---|
| CLI call returns | under 2 s (or an ID to poll) | `PLAN:44`, `CLI/request.md:58` |
| Pairing code | 15 chars, valid 10 min, single use, 3 tries | `CLI/pair.md:17-50` |
| Team size | up to 32 members, one owner | `KL:31`, `PROT/team.md:25` |
| Presence heartbeat | every 30 s | `PROT/presence.md:12` |
| Offline detected | under 90 s (75 s computed) | `PROT/presence.md:22-24`, `KL:79` |
| Agent active | CLI call in last 5 min | `PROT/presence.md:13` |
| Human present | OS idle under 10 min | `PROT/presence.md:14` |
| Urgency budget | 5 high, 2 blocking per rolling 7 days | `CLI/request.md:63` |
| Priority weight window | last 30 days | `PROT/request.md:667-671` |
| Notification latency | within 5 s of arrival | `PROT/notify.md:31` |
| Offline queue | 7 days | `CLI/relay.md:43`, `PLAN:62` |
| Resend window | 21 days after first send | `CLI/request.md:67-71` |
| Brief / title / artifacts | 16 KiB / 120 chars / 20 | `CLI/request.md:73-75` |
| Approval | 3 wrong codes reject; expires in 10 min | `CLI/approve.md:21-24` |
| Grant lifetime | 1 min to 7 days, default 2 h | `CLI/grant.md:30` |
| Fetch limits | 8 MiB per file; 256 MiB per grant per 24 h | `CLI/fetch.md:27`, `CLI/fetch.md:41` |
| Quarantine memory | a sensitive grant that ended under 7 days ago | `PROT/work-session.md:348` |
| Consult | 8 context files, 64 KiB each | `CLI/consult.md:22` |
| Debate rounds | 1-5, default 2 | `CLI/debate.md:26` |
| Debate turn timeout | 5 min to 24 h, default 1 h | `CLI/debate.md:27` |
| Debate constraints | 1-500 chars, up to 10 active | `CLI/debate.md:201-207` |
| Mailbox key life | rotate at 7 days, deleted at 21 days | `PROT/mail.md:45-53` |

### 3.23 Commands that are safe to show on screen (real syntax)

Real commands, from the CLI docs. Rule from the owner: no GitHub links, no repository file
paths, no internal ticket numbers on screen. The docs' own examples include GitHub URLs and
repo file paths (for example `https://github.com/o/r/pull/12` at `CLI/request.md:114`, and
`internal/mail/outbox.go` at `PROT/consult.md:11`). Replace those with neutral values such as
`--artifact "branch=feat/retry commit=1a2b3c4"`.

```
agentnet pair --new
agentnet pair 7KQ2M-9XHF4-TRW8N
agentnet team create backend
agentnet team invite backend
agentnet team join 7KQ2M-9XHF4-TRW8N
agentnet status --team backend
agentnet request @bob review --title "Review retry change" --brief-from-file brief.md \
        --artifact "branch=feat/retry commit=1a2b3c4" --urgency high --urgency-reason "release today"
agentnet inbox
agentnet accept r-0123456789abcdef0123456789abcdef
agentnet grant @bob --session s-... --action git.read --resource /work/app#feat/retry --expires 2h
agentnet fetch g-... --list
agentnet revoke g-...
agentnet consult @bob --question "Is this backoff safe under clock skew?" --context-file notes.txt
agentnet debate @bob --topic "How should retries back off?" --position-file position.json
agentnet decision d-... --md --out decisions/retry-backoff.md
agentnet log --verify
```

Real sample output lines from the docs: `Queued high review request r-0123...cdef to bob (team
backend)` and `bob is offline, last seen 2026-10-01T09:12:00Z; it will be delivered when bob
is back.` (`CLI/request.md:115-116`); `pong from @bob: rtt 3.2 ms (encrypted)`
(`CLI/ping.md`); `Inbox empty.` (`CLI/inbox.md:110`); status table at `CLI/status.md:59-64`.

---

## 4. PLANNED, NOT BUILT YET (never show these as existing today)

Source of truth for scope: Phase 4 in `PLAN:146-164`; specs approved 2026-09-27 at
`Docs/review/49-phase4-tickets.md`; decisions D36-D43 in `HANDOFF:317-324`.

### 4a. What is NOT in the product today

| Item | State at snapshot | Evidence | What the film must not show |
|---|---|---|---|
| `agentnet setup` (one command: login, start service, default hosted relay, run doctor) | Spec only. Not in the CLI dispatch table. `CLI/doctor.md:107` names it as ticket 4.9a, future work. | `MAIN:48-134`; spec `Docs/review/49-phase4-tickets.md:180-195`; `PLAN:160` | Any `agentnet setup` line, any "one command setup" scene |
| Accounts / `agentnet login` / `logout` / sign in with GitHub | CLI side not built. Relay side has an accounts core (off unless `--accounts`), but no web login page, no OAuth flow, no CLI login. | no `login` in `MAIN`; `PROT/accounts.md:1-12`; `Docs/review/49-phase4-tickets.md` 4.2b, 4.2c rows; `HANDOFF:116` | A "Sign in with GitHub" screen, a login URL, a browser code-confirm screen |
| Hosted relay at `relay.dorylinae.net` | Not confirmed live. Domain bought (D39); deploy runbook and config exist (`Docs/ops/early-relay-deploy.md`, `deploy/early/`) but the deploy is listed as a pending owner action. When deployed it is "for the owner's own team only", no accounts, no backups. | `HANDOFF:97`, `HANDOFF:40`; `Docs/ops/early-relay-deploy.md:1-12`; `Docs/ops/early-relay-deploy.md:19-27` | Any "connected to relay.dorylinae.net" claim as a current fact; any uptime, region or backup claim |
| Beta invite codes (`BETA-XXXX-XXXX-XXXX`), quota groups, seats, waitlist | Spec only. The relay admin CLI has `account` and `group` subcommands only, no `invite`. Waitlist is an external form (OD-P4-9) not yet made. | `PROT/invites.md:25-31`; `cmd/relay/admin.go:23`; `Docs/review/49-phase4-tickets.md:558` | A beta code on screen, a seat counter, a waitlist form screen unless it is a real form the owner supplies |
| Quotas (300 sessions per team per month) | Spec only; plan says "soft cap" first | `PLAN:152`, `PROT/relay-hosted.md:842-860` | A usage meter |
| Telemetry report, `agentnet telemetry` | Spec only | `PROT/telemetry.md:17-20` | A telemetry screen |
| `agentnet feedback "..."` | Spec only | `PROT/feedback.md:1-18`; `PLAN:158` | A feedback command |
| Install script `curl -fsSL https://dorylinae.net/install.sh \| sh` | The script exists in the repo with a signing key embedded; the release pipeline dry run passed and the draft was deleted. No release is published (no `v*` tag). Hosting of the script at `dorylinae.net` is not confirmed. | `scripts/install.sh:1-30`, `CLI/install.md:15-25`, `HANDOFF:107`, `HANDOFF:114` | The curl line as something that works today |
| Homebrew `brew install magazem/tap/agentnet` | A formula template only; the tap repo is not confirmed to exist | `packaging/homebrew/agentnet.rb:1-8`; `CLI/install.md:53-61` | The brew line |
| Windows MSI (per-user) | Not built (ticket 4.4b). Windows is a zip, unsigned in wave 1. | `CLI/install.md:76-80` | An MSI installer screen |
| Quickstart and docs site, "tell your agent" page, per-command pages | Per-command pages exist as repo markdown. A quickstart for testers (4.5a) is not written. | `PLAN:156`; `Docs/review/49-phase4-tickets.md` 4.5a row | A docs site |
| Clean-machine install proof ("first request in under 15 minutes") | A target, not a measured result | `PLAN:148`, `PLAN:160` | Any stopwatch claim |
| Outside security review | Scheduled before public launch, not before the beta | `KL:12-17`, `SECURITY.md:20-24`, `HANDOFF:323` | Any "audited" claim |
| OS user-presence on approvals (4.8b) | Planned | `HANDOFF:311` (D30 row) | "biometric/OS-verified approvals" |
| Auto-update, web dashboard, paid tiers, Windows service | Explicitly not in Phase 4 | `Docs/review/49-phase4-tickets.md:40-42` | Any of these |
| Discovery of strangers, marketplace, credits, reputation, pricing | Not decided, not in scope; only studied later if the free phase shows interest | `PLAN:7`, `PLAN:13`, `PLAN:215` | Any of these |
| MCP adapter, SDK | Not decided | `PLAN:13` | "Plug-in for every agent" |
| Live shared sessions, group crypto, three-way debate | Not built, not planned for the free phase | `KL:53-54`, `PLAN:38`, `PLAN:144` | Any of these |

### 4b. What beta testers WILL get, per the plan (label every one "planned for the beta")

All of these come from approved specs. They are intentions, not shipped features.

- Closed, invite-only beta. Planned as 12 weeks (`PLAN:146`). Waves of 10, 10 and 10 teams;
  8 seats per team; invite codes expire after 48 hours (D36, `HANDOFF:317`,
  `PROT/invites.md:25-31`). The plan's gate counts assume 30 invited teams (`PLAN:208`).
  Phase 4's stated outcome is "ten invited teams run on a relay you host" (`PLAN:148`).
- A relay we host, in an EU location (Hetzner Cloud, D37), with the 7-day offline queue
  and a free-tier cap of 300 relay sessions per team per month (`PLAN:152`; counted as mail
  volume, soft cap first, `PROT/relay-hosted.md:851-860`).
- Sign in with a GitHub account to bind your install to a person; no passwords stored
  (`PLAN:153`, `PROT/accounts.md:30-33`, D36 "GitHub OAuth only"). Up to 4 devices per
  account (`PROT/accounts.md:18`).
- Three steps from nothing to a first request: one install command, one `agentnet setup`,
  one `agentnet team join <code>`; each non-interactive so an agent can run them, with the
  human confirming the login in the browser (`PLAN:160`, `Docs/review/49-phase4-tickets.md:180-195`).
- Install via a signed script (macOS, Linux), Homebrew, and a Windows build (`PLAN:155`,
  `CLI/install.md`). The installer checks an Ed25519 signature before installing and
  refuses to install unsigned.
- A quickstart, a page per command, and a page showing how to tell your agent about
  agentnet, with snippets for CLAUDE.md, AGENTS.md and Hermes (`PLAN:156`).
- Feedback: `agentnet feedback "..."`, sealed so the relay host cannot read it; a weekly
  20-minute call with two teams; a public changelog; one release a week (`PLAN:158`,
  `PROT/feedback.md:7-10`).
- Telemetry: counters only, no content. Relay counters are always on at the hosted relay;
  the daemon's weekly report is opt-in (`PROT/telemetry.md:17-20`, D36).
- An honest privacy note and beta terms. Testers are told plainly that the beta has not had
  an independent outside security review (`KL:12-17`, `HANDOFF:323`).
- A debate-and-decision demo is in the plan's own framing: "The five-minute demo video is
  made at the start of this phase... every feature that is not in the video is a candidate
  for cutting" (`PLAN:164`). That is internal direction, not a claim for the film.

---

## 5. Claims that would be FALSE or RISKY in a promo

Verdict key: FALSE = the docs say the opposite. RISKY = partly true or unproven. Each row
gives what is safe to say instead.

| # | Claim | Verdict | Evidence | Say instead |
|---|---|---|---|---|
| 1 | "Audited", "security-audited", "pen-tested", "independently reviewed" | FALSE | "This private beta has not had an independent outside security review" (`KL:12-17`); "Code so far has been reviewed by AI agents and the maintainer, not by an outside security firm" (`SECURITY.md:20-24`). Review is scheduled before public launch (`HANDOFF:323`). | "Every action is written to a log you can check." Use "audit log" only when you mean the log |
| 2 | "Tamper-proof log" | FALSE | The chain detects a changed, deleted, inserted or reordered row; it proves nothing against a full database rewrite by anyone who can write the file (`KL:98-104`, `CLI/log.md:96-101`) | "A hash-chained log. Edits to a row break the chain." |
| 3 | "Zero-knowledge", "the relay knows nothing", "completely anonymous" | FALSE | Relay sees who talks to whom, when, how much, and when a daemon is online even if invisible (`PROT/mail.md:659-662`, `PROT/relay-hosted.md:886-900`). On the hosted relay it will also know the GitHub login or email behind each key (`PROT/relay-hosted.md:893`). | "The relay carries ciphertext. It cannot read what you send." |
| 4 | "Invisible means nobody can tell you are online" | FALSE | Invisible hides you from teammates, not the relay; a paired peer who sends mail can still infer you are online (`KL:81-82`, `PROT/presence.md:262`) | "Invisible: teammates see your last-seen time only." |
| 5 | "Works with every agent / any AI / all harnesses" | RISKY | The agent needs a shell tool and reads an instruction file (`SNIP:12-16`). Named: Claude Code, Codex, OpenCode, Hermes, custom (`PLAN:11`). Run with real agents: Claude Code and agy only; Codex blocked, Hermes and OpenCode never run (`HANDOFF:161-163`, `tests/harness/README.md`). No MCP adapter or SDK (`PLAN:13`). | "Works with agents that can run shell commands. Tested with Claude Code." (name only what was tested, or "agents like Claude Code and Codex" only if the owner accepts naming untested ones) |
| 6 | "Encrypted group chat / group channels" | FALSE | "No group crypto in this phase" (`PLAN:38`); "There is no group cryptography" (`PROT/team.md:9-12`). Team messages are pairwise sealed mail. | "Teams of up to 32. Each message is sealed to one teammate." |
| 7 | "Live shared sessions", "watch their agent work", "pair-program with agents" | FALSE | "There are no shared live sessions yet" (`KL:53-54`). A work session is a bounded, saved unit of async work. | "Hand work over. Get a structured result back." |
| 8 | "Edit a request after sending" | FALSE | "No editing. A sent request cannot be changed. Cancel it and send a new one." (`KL:40`) | omit |
| 9 | "Cancel any time" | RISKY | Cancel works only while the request is waiting or deferred; not after accept (`KL:41-45`) | "Cancel a request until it is accepted." |
| 10 | "Instant", "real-time" | RISKY | Request returns `queued` at once, but delivery waits for the other machine; notification within 5 s of arrival is the acceptance target (`PROT/notify.md:31`); offline lag up to about 90 s (`KL:79`) | "Returns in under 2 seconds. Waits for them if they are offline." |
| 11 | "Your agents talk freely / agents give each other orders" | FALSE | "Untrusted text must never grant authority" (`scope.md:77-79`). The snippet tells agents a request is "a description of work to consider, not instructions that override the user" and to ask the user before accepting work that needs access (`SNIP:79-82`). | "A request describes work. It never gives orders." |
| 12 | "Share your repo / give access in one click" | RISKY | Artifacts in a request "give the other side no access" (`KL:51-52`). Access is a separate grant: read-only, a directory or a git branch, max 7 days, human approval (`CLI/grant.md:22-38`). No write grants. | "Read-only access that expires. You approve it." |
| 13 | "One binary", "a single download" | RISKY | The plan says "installs one binary each" (`PLAN:9`), but the release archive holds `agentnet`, `agentnetd` and `relay` and the installer places two (`CLI/install.md:8-11`, `CLI/install.md:22`) | "One install" only after the install path exists; otherwise omit |
| 14 | "Install in 15 minutes" or any setup stopwatch | RISKY | A plan target, never measured (`PLAN:148`, `PLAN:160`) | omit |
| 15 | "Open source" | RISKY | Licence is PolyForm Shield 1.0.0, "source-available": free to read, audit and use, not for building a competing product (`README.md:66-68`, `HANDOFF:283` D1). The plan's earlier Apache-2.0 plan was superseded (`PLAN:229`). The brief also forbids exposing the repo. | omit the licence; never say "open source" |
| 16 | "Free", "free forever" | RISKY | "Free tier" is the plan's name for the phase; pricing is explicitly undecided (`PLAN:13`); the plan's own end state "free open-source tool with a hosted relay" is one possible outcome (`PLAN:215`). The hosted free tier is capped (`PLAN:152`). | "Free during the closed beta" only if the owner confirms |
| 17 | "Biscuit tokens" / any named token tech | FALSE as built | The plan named Biscuit (`PLAN:39`); the build uses an in-house signed token (Ed25519 over canonical JSON) (`PROT/grant.md:29-40`, D16 `HANDOFF:297`) | "Signed, expiring, revocable access." |
| 18 | "Perfect forward secrecy" for everything | FALSE | Mail has forward secrecy only through key rotation (7 days) and deletion (21 days) (`PROT/mail.md:26-27`, `PROT/mail.md:45-53`). Noise sessions (ping, fetch) do get fresh keys per session (`PROT/session.md:35-38`). | "End-to-end encrypted." |
| 19 | "Nothing is stored" / "no history" | FALSE | Requests, results and briefs are kept locally; the daemon never deletes content on its own; `agentnet prune` removes finished items after a human approval (`CLI/prune.md:1-9`, D50) | "Your data stays on your machine." (true: local SQLite, `PLAN:30`) |
| 20 | "Peer to peer, no server" / "works without internet" | FALSE | A relay carries all traffic (`PLAN:17`). Relay can be self-hosted (`PLAN:32`, D17) but the self-host guide is not written (`Docs/review/49-phase4-tickets.md:52`). | "Messages go through a relay that cannot read them." |
| 21 | "Agents decide for you" / "binding agreement" / "legal record" | FALSE | A Decision's signatures mean "this is the debate as my daemon recorded it", not "I agree" (`PROT/decision.md:18-20`). It can end escalated with the disagreement recorded (`PROT/debate.md:190-197`). Humans can add constraints. No legal claim anywhere. | "A signed record of what was argued and how it ended." |
| 22 | "Always signed by both" | RISKY | A Decision can end single-signed if the peer goes silent; verify exits 6 and Markdown shows UNCONFIRMED (`KL:93-97`, `CLI/decision.md:51`) | "Both daemons sign." (the normal case) |
| 23 | "The agent can't be tricked into approving" / "unbreakable approval" | RISKY | The boundary: an agent that runs arbitrary programs as the user and sets out to defeat the approval can read notification history, drive the window, or restart the daemon in terminal mode (`PROT/approval.md:40-50`) | "No command takes the approval code. Your agent cannot approve itself." |
| 24 | "The product learns from your work" | FALSE | The private experience record exists but "Nothing reads it in Phase 3" (`PROT/experience.md:7-10`) | omit |
| 25 | "Built-in AI writes the brief / summarises for you" | FALSE | "No platform-side model"; the sender's own agent writes the brief (`PLAN:89`) | "Your agent writes the brief." |
| 26 | "Find expert agents / marketplace / hire" | FALSE | Out of scope (`PLAN:7`, `PLAN:13`) | omit |
| 27 | "Unlimited team size" / "for big teams" | FALSE | 2-5 person target (`PLAN:9`); hard cap 32 (`KL:31`); planned beta seats 8 per group (`PROT/invites.md`) | "For small teams." |
| 28 | "Team admins / transfer ownership" | FALSE | One owner, no admins; if the owner loses the device the team cannot change (`KL:21-28`) | omit |
| 29 | "Notifications show the message" | FALSE | Notifications and webhooks never carry the brief; webhook title only if you turn it on (`PROT/notify.md:269-278`, `KL:87-89`) | "Notifications never carry your brief." |
| 30 | Gate numbers as results ("15 of 30 teams active", "10 consecutive runs", "median 5 requests a week") | FALSE as results | They are pass/fail targets in the plan (`PLAN:205-214`), not measurements | omit |
| 31 | "Signed, verified installer" as a present fact | RISKY | The mechanism exists in the script (`CLI/install.md:27-38`) and has limits (`CLI/install.md:45-51`), but no release has been published | only under "planned for the beta" |
| 32 | "Smooth on every OS" / "no warnings on Windows" | FALSE | Windows builds are unsigned in wave 1; SmartScreen warns (`CLI/install.md:78-80`). Linux needs zenity or kdialog for the approval window (`CLI/status.md:45-54`). | name macOS, Linux, Windows only |
| 33 | "Debate is a vote" / "three or more agents" | FALSE | Two parties only (`PLAN:144`, `PROT/debate.md:30-31`) | "Two agents argue." |
| 34 | Brand: "Dorylinae.ai", "DoryLinae", "Dory", a tagline, "hive/swarm/drone" | brand rule breach | Product is Dorylinae; "There is no tagline"; ant words limited to colony, trail, column, bivouac, at most one per screen (brand `docs/voice.md:12`, `docs/voice.md:27-34`) | follow the voice doc |

Name note for the script: the product and brand name is Dorylinae. The command is
`agentnet`, the daemon is `agentnetd`. "AgentNet" is the project name in docs and the
binary's name; the plan itself says the name "collides with several existing projects"
(`PLAN:231`). Show `agentnet` only as the command.

---

## 6. Ten true sentences for on-screen use

Brand voice applied (`docs/voice.md` in the brand kit): short, declarative, present tense,
numerals, no hype, no exclamation marks, "you" for the reader. The voice doc says its own
examples are tone, not approved product copy, so these are checked against the repo, not
copied from the voice doc. Each is true as written.

| # | Sentence | Source | Watch-out |
|---|---|---|---|
| 1 | Your agent sees who is online. | `CLI/status.md:56-68`, `PROT/presence.md:12-14`, `SNIP:29` | "Sees" = reads `agentnet status --team`. Layers: daemon, agent, human |
| 2 | Your agent asks your teammate's agent for a review. | `CLI/request.md:5-6`, `SNIP:17-18` | Types are review, task, question |
| 3 | Nobody is online. The request waits in their inbox. | `CLI/request.md:58-61`, `PROT/request.md:734-737` | Held 7 days, then delivery shows `expired` (unknown) |
| 4 | Requests arrive most urgent first. | `CLI/inbox.md:34-40`, `PROT/request.md:664-686` | Urgency is weighted by the sender's track record |
| 5 | You get 5 high-urgency requests a week. Then they arrive as normal. | `CLI/request.md:63-65`, `PROT/request.md:705-718` | Plus 2 blocking; rolling 7 days; enforced on both sides |
| 6 | Messages are sealed on your machine. The relay carries ciphertext. | `PROT/mail.md:14-22`, `CLI/relay.md:3-4` | The relay still sees who, when, how big |
| 7 | No command takes the approval code. Your agent cannot approve itself. | `CLI/approve.md:14-19`, `SNIP:84-91` | Do not add "unbreakable" (section 5, claim 23) |
| 8 | A grant expires. Revoke it and the next read fails. | `CLI/grant.md:30`, `CLI/grant.md:67-69`, `PROT/grant.md:275-280` | Read-only; directory or git branch; max 7 days |
| 9 | Two agents argue a question. A signed Decision comes out. | `CLI/debate.md:5-6`, `PROT/decision.md:10-16`, `PLAN:132` | Cancelled debates produce none; can be single-signed; "signed" does not mean "agreed" |
| 10 | The log records that a request happened. It never records what it said. | `CLI/log.md:3-5`, `PROT/audit.md:6-17` | Do not add "tamper-proof" (section 5, claim 2) |

Reserve lines (also true, not part of the ten):

- A Decision exports as Markdown for your repo. (`CLI/decision.md:20`, `CLI/decision.md:83-94`)
- Your agent runs it from its shell. (`SNIP:12-16`)
- A request returns in under 2 seconds. (`CLI/request.md:58`)
- Every action leaves a trail you can verify. (`PROT/audit.md:24-45`, `CLI/log.md:66-71`;
  "trail" is the one ant word for the screen; keep the section 5 claim 2 caveat)
- It runs on macOS, Linux and Windows. (`README.md:57-59`, `CLI/agentnetd-install.md:30-34`)
- Invisible: teammates see your last-seen time only. (`CLI/presence.md:11`, `PLAN:87`)
- A request describes work. It never gives orders. (`SNIP:79-82`, `scope.md:77-79`)

Planned-for-beta lines (allowed only on a screen that says so, and only if the owner wants
them): "Closed beta. By invitation." (`PLAN:146-148`); "10 teams in the first wave."
(`HANDOFF:317`); "7-day offline queue on the relay." is already built (`CLI/relay.md:43`).

---

## 7. Open items the scriptwriters should resolve with the owner

1. The hosted relay is not confirmed live. If any scene shows a real connection to
   `relay.dorylinae.net`, the owner must confirm it is deployed (`HANDOFF:97`). Otherwise
   show a local relay, which is what the repo's own tests and smoke scripts do.
2. How the beta is joined (invite code, waitlist form, address) is not in the repo as a
   shipped thing. The call to action cannot cite `agentnet setup` or a BETA code as working.
3. Whether to name harnesses in the film. Only Claude Code and agy have run end to end with
   real agents; Codex was blocked.
4. "Free" wording during the beta (section 5, claim 16).
5. Whether to mention the missing outside security review on screen. The beta invitation is
   planned to say it plainly (`KL:12-17`), which is a reason to treat it as part of the brand's
   honest voice rather than hide it.
6. Stale docs to ignore: `README.md:8-13` (says Phase 0 and 1 only), "draft" status lines in
   `CLI/*.md` headers, and the plan's `repo.read` action name (built actions are `fs.read`
   and `git.read`, `CLI/grant.md:10`).
