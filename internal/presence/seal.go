package presence

import (
	"crypto/ed25519"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// SealInput describes one presence heartbeat to seal.
type SealInput struct {
	Priv       ed25519.PrivateKey // sender identity key
	To         string             // recipient identity key, wire form
	MailboxPub []byte             // recipient's newest mailbox public key, 32 bytes
	Created    time.Time
	Body       Body
}

// Seal builds the padded body and seals it as a presence message
// (Docs/protocol/presence.md §Envelope, §Body), reusing the mail seal.
func Seal(in SealInput) (mail.Sealed, error) {
	from := envelope.KeyString(in.Priv.Public().(ed25519.PublicKey))
	id := mail.NewPresenceID()
	body, err := Build(in.Body, from, in.To, id, in.Created)
	if err != nil {
		return mail.Sealed{}, err
	}
	return mail.SealPresence(mail.SealInput{
		Priv: in.Priv, To: in.To, MailboxPub: in.MailboxPub, ID: id,
		Kind: Kind, Body: body, Created: in.Created,
	})
}
