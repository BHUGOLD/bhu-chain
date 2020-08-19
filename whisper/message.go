package whisper

import (
	"crypto/ecdsa"
	"math/rand"
	"time"

	"github.com/bhu/bhu-chain/common"
	"github.com/bhu/bhu-chain/crypto"
	"github.com/bhu/bhu-chain/logger"
	"github.com/bhu/bhu-chain/logger/glog"
)

type Message struct {
	Flags     byte
	Signature []byte
	Payload   []byte

	Sent time.Time
	TTL  time.Duration

	To   *ecdsa.PublicKey
	Hash common.Hash
}

type Options struct {
	From   *ecdsa.PrivateKey
	To     *ecdsa.PublicKey
	TTL    time.Duration
	Topics []Topic
}

func NewMessage(payload []byte) *Message {

	flags := byte(rand.Intn(256))
	flags &= ^signatureFlag

	return &Message{
		Flags:   flags,
		Payload: payload,
		Sent:    time.Now(),
	}
}

func (self *Message) Wrap(BHU time.Duration, options Options) (*Envelope, error) {

	if options.TTL == 0 {
		options.TTL = DefaultTTL
	}
	self.TTL = options.TTL

	if options.From != nil {
		if err := self.sign(options.From); err != nil {
			return nil, err
		}
	}
	if options.To != nil {
		if err := self.encrypt(options.To); err != nil {
			return nil, err
		}
	}

	envelope := NewEnvelope(options.TTL, options.Topics, self)
	envelope.Seal(BHU)

	return envelope, nil
}

func (self *Message) sign(key *ecdsa.PrivateKey) (err error) {
	self.Flags |= signatureFlag
	self.Signature, err = crypto.Sign(self.hash(), key)
	return
}

func (self *Message) Recover() *ecdsa.PublicKey {
	defer func() { recover() }()

	if self.Signature == nil {
		return nil
	}

	pub, err := crypto.SigToPub(self.hash(), self.Signature)
	if err != nil {
		glog.V(logger.Error).Infof("Could not get public key from signature: %v", err)
		return nil
	}
	return pub
}

func (self *Message) encrypt(key *ecdsa.PublicKey) (err error) {
	self.Payload, err = crypto.Encrypt(key, self.Payload)
	return
}

func (self *Message) decrypt(key *ecdsa.PrivateKey) error {
	cleartext, err := crypto.Decrypt(key, self.Payload)
	if err == nil {
		self.Payload = cleartext
	}
	return err
}

func (self *Message) hash() []byte {
	return crypto.Sha3(append([]byte{self.Flags}, self.Payload...))
}

func (self *Message) bytes() []byte {
	return append([]byte{self.Flags}, append(self.Signature, self.Payload...)...)
}
