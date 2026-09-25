# Decision d-7eaeb0b78e6bd96981357b500af94044 : ` Outbox retry policy `

- Outcome: agreed (accepted) ; Signed by: initiator and respondent
- Participants: initiator ` alice-agent ` (fingerprint AB12-CD34-EF56), respondent ` bob-agent ` (fingerprint 12AB-34CD-56EF)
- Session s-36375782ceb6baea9cee4d4273dfb035, request r-0123456789abcdef0123456789abcdef, team t-00112233445566778899aabbccddeeff, opened 2026-10-01T09:00:00Z, closed 2026-10-01T09:20:00Z, hash 6ec367cd5f0f82b1d929878e678ba1aafdd3c33cb13dc22da2fba55836094ede

## Problem

```
How should the outbox retry?
```


Context files:
- ` outbox.go ` (4096 bytes, sha256 abababababababababababababababababababababababababababababababab)

## Initial positions

### Initiator

Claim: ` Use capped exponential backoff for outbox retries `
Assumptions:
- ` Clock skew is under 5 s `
Evidence:
- ` file `: ` internal/mail/outbox.go ` (note: ` the retry loop `)
Rejected alternatives:
- ` Fixed 30 s retry ` — ` Floods the relay after an outage `

Argument:
```
Retries should back off exponentially, capped at 10 minutes.
```

### Respondent

Claim: ` Add full jitter to the existing backoff `
Assumptions:
- ` Clock skew is under 5 s `
Evidence:
- ` file `: ` internal/mail/outbox.go ` (note: ` the retry loop `)
Rejected alternatives:
- ` Fixed 30 s retry ` — ` Floods the relay after an outage `

Argument:
```
Jitter matters more than the curve.
```

## Rounds

### Round 1

#### Initiator

(pass)

#### Respondent

(pass)

## Final agreement

Decision: ` Capped exponential backoff with full jitter `

## Human decisions and constraints

- c-00000000000000000000000000000001 (initiator, 2026-10-01T09:10:00Z): ` Ship behind a feature flag ` — approved on the initiator's machine; the other side cannot check this

## Verification

Hash: 6ec367cd5f0f82b1d929878e678ba1aafdd3c33cb13dc22da2fba55836094ede
Signature (initiator): oU23UZtZYRFm7RVkDdqPfoFfL6ZFlJj8K4Xqq4ySy1EtDdeO58GEo148fZbERBYbW-75C5erU6AhqoDwiDBnAw
Signature (respondent): xCdaoO0EujU6LbZJOgvIX_FvFlEikdlftxbUmUF9ef_1xTMrqScV_9UDGrsQu8aO_H7adF331lOzjs6PTJv-Cg
Verify with `agentnet decision verify d-7eaeb0b78e6bd96981357b500af94044.json`
