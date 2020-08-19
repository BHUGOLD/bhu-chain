package whisper

import (
	"crypto/ecdsa"
	"sync"
	"time"

	"github.com/bhu/bhu-chain/common"
	"github.com/bhu/bhu-chain/crypto"
	"github.com/bhu/bhu-chain/crypto/ecies"
	"github.com/bhu/bhu-chain/event/filter"
	"github.com/bhu/bhu-chain/logger"
	"github.com/bhu/bhu-chain/logger/glog"
	"github.com/bhu/bhu-chain/p2p"
	"gopkg.in/fatih/set.v0"
)

const (
	statusCode   = 0x00
	messagesCode = 0x01

	protocolVersion uint64 = 0x02
	protocolName           = "shh"

	signatureFlag   = byte(1 << 7)
	signatureLength = 65

	expirationCycle   = 800 * time.Millisecond
	transmissionCycle = 300 * time.Millisecond
)

const (
	DefaultTTL = 50 * time.Second
	DefaultBHU = 50 * time.Millisecond
)

type MessageEvent struct {
	To      *ecdsa.PrivateKey
	From    *ecdsa.PublicKey
	Message *Message
}

type Whisper struct {
	protocol p2p.Protocol
	filters  *filter.Filters

	keys map[string]*ecdsa.PrivateKey

	messages    map[common.Hash]*Envelope
	expirations map[uint32]*set.SetNonTS
	poolMu      sync.RWMutex

	peers  map[*peer]struct{}
	peerMu sync.RWMutex

	quit chan struct{}
}

func New() *Whisper {
	whisper := &Whisper{
		filters:     filter.New(),
		keys:        make(map[string]*ecdsa.PrivateKey),
		messages:    make(map[common.Hash]*Envelope),
		expirations: make(map[uint32]*set.SetNonTS),
		peers:       make(map[*peer]struct{}),
		quit:        make(chan struct{}),
	}
	whisper.filters.Start()

	whisper.protocol = p2p.Protocol{
		Name:    protocolName,
		Version: uint(protocolVersion),
		Length:  2,
		Run:     whisper.handlePeer,
	}

	return whisper
}

func (self *Whisper) Protocol() p2p.Protocol {
	return self.protocol
}

func (self *Whisper) Version() uint {
	return self.protocol.Version
}

func (self *Whisper) NewIdentity() *ecdsa.PrivateKey {
	key, err := crypto.GenerateKey()
	if err != nil {
		panic(err)
	}
	self.keys[string(crypto.FromECDSAPub(&key.PublicKey))] = key

	return key
}

func (self *Whisper) HasIdentity(key *ecdsa.PublicKey) bool {
	return self.keys[string(crypto.FromECDSAPub(key))] != nil
}

func (self *Whisper) GetIdentity(key *ecdsa.PublicKey) *ecdsa.PrivateKey {
	return self.keys[string(crypto.FromECDSAPub(key))]
}

func (self *Whisper) Watch(options Filter) int {
	filter := filterer{
		to:      string(crypto.FromECDSAPub(options.To)),
		from:    string(crypto.FromECDSAPub(options.From)),
		matcher: newTopicMatcher(options.Topics...),
		fn: func(data interface{}) {
			options.Fn(data.(*Message))
		},
	}
	return self.filters.Install(filter)
}

func (self *Whisper) Unwatch(id int) {
	self.filters.Uninstall(id)
}

func (self *Whisper) Send(envelope *Envelope) error {
	return self.add(envelope)
}

func (self *Whisper) Start() {
	glog.V(logger.Info).Infoln("Whisper started")
	go self.update()
}

func (self *Whisper) Stop() {
	close(self.quit)
	glog.V(logger.Info).Infoln("Whisper stopped")
}

func (self *Whisper) Messages(id int) []*Message {
	messages := make([]*Message, 0)
	if filter := self.filters.Get(id); filter != nil {
		for _, envelope := range self.messages {
			if message := self.open(envelope); message != nil {
				if self.filters.Match(filter, createFilter(message, envelope.Topics)) {
					messages = append(messages, message)
				}
			}
		}
	}
	return messages
}

func (self *Whisper) handlePeer(peer *p2p.Peer, rw p2p.MsgReadWriter) error {

	whisperPeer := newPeer(self, peer, rw)

	self.peerMu.Lock()
	self.peers[whisperPeer] = struct{}{}
	self.peerMu.Unlock()

	defer func() {
		self.peerMu.Lock()
		delete(self.peers, whisperPeer)
		self.peerMu.Unlock()
	}()

	if err := whisperPeer.handshake(); err != nil {
		return err
	}
	whisperPeer.start()
	defer whisperPeer.stop()

	for {

		packet, err := rw.ReadMsg()
		if err != nil {
			return err
		}
		var envelopes []*Envelope
		if err := packet.Decode(&envelopes); err != nil {
			glog.V(logger.Info).Infof("%v: failed to decode envelope: %v", peer, err)
			continue
		}

		for _, envelope := range envelopes {
			if err := self.add(envelope); err != nil {

				glog.V(logger.Debug).Infof("%v: failed to pool envelope: %v", peer, err)
			}
			whisperPeer.mark(envelope)
		}
	}
}

func (self *Whisper) add(envelope *Envelope) error {
	self.poolMu.Lock()
	defer self.poolMu.Unlock()

	hash := envelope.Hash()
	if _, ok := self.messages[hash]; ok {
		glog.V(logger.Detail).Infof("whisper envelope already cached: %x\n", envelope)
		return nil
	}
	self.messages[hash] = envelope

	if self.expirations[envelope.Expiry] == nil {
		self.expirations[envelope.Expiry] = set.NewNonTS()
	}
	if !self.expirations[envelope.Expiry].Has(hash) {
		self.expirations[envelope.Expiry].Add(hash)

		go self.postEvent(envelope)
	}
	glog.V(logger.Detail).Infof("cached whisper envelope %x\n", envelope)

	return nil
}

func (self *Whisper) postEvent(envelope *Envelope) {
	if message := self.open(envelope); message != nil {
		self.filters.Notify(createFilter(message, envelope.Topics), message)
	}
}

func (self *Whisper) open(envelope *Envelope) *Message {

	if len(self.keys) == 0 {
		if message, err := envelope.Open(nil); err == nil {
			return message
		}
	}

	for _, key := range self.keys {
		message, err := envelope.Open(key)
		if err == nil {
			message.To = &key.PublicKey
			return message
		} else if err == ecies.ErrInvalidPublicKey {
			return message
		}
	}

	return nil
}

func createFilter(message *Message, topics []Topic) filter.Filter {
	matcher := make([][]Topic, len(topics))
	for i, topic := range topics {
		matcher[i] = []Topic{topic}
	}
	return filterer{
		to:      string(crypto.FromECDSAPub(message.To)),
		from:    string(crypto.FromECDSAPub(message.Recover())),
		matcher: newTopicMatcher(matcher...),
	}
}

func (self *Whisper) update() {

	expire := time.NewTicker(expirationCycle)

	for {
		select {
		case <-expire.C:
			self.expire()

		case <-self.quit:
			return
		}
	}
}

func (self *Whisper) expire() {
	self.poolMu.Lock()
	defer self.poolMu.Unlock()

	now := uint32(time.Now().Unix())
	for then, hashSet := range self.expirations {

		if then > now {
			continue
		}

		hashSet.Each(func(v interface{}) bool {
			delete(self.messages, v.(common.Hash))
			return true
		})
		self.expirations[then].Clear()
	}
}

func (self *Whisper) envelopes() []*Envelope {
	self.poolMu.RLock()
	defer self.poolMu.RUnlock()

	envelopes := make([]*Envelope, 0, len(self.messages))
	for _, envelope := range self.messages {
		envelopes = append(envelopes, envelope)
	}
	return envelopes
}
