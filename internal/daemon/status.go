package daemon

import (
	"context"
	"sort"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/presence"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// relayState is the "presence.relay" value of Docs/protocol/ipc.md §status.
func relayState(client *relayclient.Client, relayURL string) string {
	if relayURL == "" {
		return "none"
	}
	if client == nil || !client.Connected() {
		return "disconnected"
	}
	if !client.HasFeature(envelope.FeatureEphemeral) {
		return "unsupported"
	}
	return "connected"
}

// presenceStatus builds the own-values "presence" object of "status"
// (Docs/protocol/ipc.md §status). 1.2c only ever reports mode "visible":
// the other modes are 1.3.
func presenceStatus(ctx context.Context, client *relayclient.Client, relayURL string, sender *presence.Sender) PresenceStatus {
	return PresenceStatus{
		Mode:         "visible",
		Relay:        relayState(client, relayURL),
		AgentActive:  sender.AgentActive(),
		HumanPresent: sender.HumanPresent(ctx),
	}
}

// statusTeam builds the "team" object of "status --team"
// (Docs/protocol/ipc.md §status): the team's members, owner first then by
// name then by public key, each with its effective presence.
func statusTeam(ctx context.Context, ts *team.Store, ps *peers.Store, pstore *presence.Store, sender *presence.Sender, selfName string, client *relayclient.Client, relayURL, ref string) (*StatusTeamResult, error) {
	t, err := resolveTeam(ctx, ts, ref)
	if err != nil {
		return nil, err
	}
	members, err := ts.Members(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	peerList, err := ps.List(ctx)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]peers.Peer, len(peerList))
	for _, pr := range peerList {
		byKey[pr.PublicKey] = pr
	}
	now := time.Now()
	views := make([]StatusTeamMember, 0, len(members))
	for _, m := range members {
		v := StatusTeamMember{PublicKey: m.Key, Owner: m.Key == t.Owner, Self: m.Key == ts.Self}
		fp, err := envelope.KeyFingerprint(m.Key)
		if err != nil {
			return nil, err
		}
		v.Fingerprint = fp
		if m.Key == ts.Self {
			v.Name = selfName
			v.DaemonOnline = relayState(client, relayURL) == "connected"
			v.AgentActive = sender.AgentActive()
			v.HumanPresent = sender.HumanPresent(ctx)
			v.LastSeen = formatStatusTime(&now)
		} else {
			if pr, ok := byKey[m.Key]; ok {
				v.Name = pr.Name
				trust := pr.Trust
				v.Trust = &trust
			}
			pv, err := pstore.View(ctx, m.Key, now)
			if err != nil {
				return nil, err
			}
			v.DaemonOnline = pv.DaemonOnline
			v.AgentActive = pv.AgentActive
			v.HumanPresent = pv.HumanPresent
			v.LastSeen = formatStatusTime(pv.LastSeen)
			v.AgentLastActive = formatStatusTime(pv.AgentLastActive)
			v.HumanLastPresent = formatStatusTime(pv.HumanLastPresent)
		}
		views = append(views, v)
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Owner != views[j].Owner {
			return views[i].Owner
		}
		if views[i].Name != views[j].Name {
			return views[i].Name < views[j].Name
		}
		return views[i].PublicKey < views[j].PublicKey
	})
	return &StatusTeamResult{ID: t.ID, Name: t.Name, Owner: t.Owner, Epoch: t.Epoch, State: t.State, Members: views}, nil
}

// formatStatusTime is RFC 3339 UTC with whole seconds, or nil
// (Docs/protocol/ipc.md §status).
func formatStatusTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Truncate(time.Second).Format(time.RFC3339)
	return &s
}
