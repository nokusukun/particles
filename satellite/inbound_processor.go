package satellite

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/perlin-network/noise"
	"github.com/perlin-network/noise/protocol"
	"github.com/perlin-network/noise/skademlia"
)

var (
	log_inbound                = "Inbound"
	_           protocol.Block = (*SatPlug)(nil)
	_           noise.Message  = (*Packet)(nil)
)

type Inbound struct {
	Peer    *noise.Peer
	Message Packet
	Payload interface{}

	totalReplies int
}

func (i *Inbound) PeerID() string {
	return hex.EncodeToString(protocol.PeerID(i.Peer).(skademlia.ID).PublicKey())
}

func (i *Inbound) As(in interface{}) interface{} {
	// TODO: Change this into something more elegant, !IMPORTANT
	b, err := json.Marshal(i.Message.Payload)
	if err != nil {
		log.Error(err)
	}

	err = json.Unmarshal(b, in)
	if err != nil {
		log.Error(err)
	}
	return in
}

func (i *Inbound) Reply(value interface{}) {
	tag := i.Message.returnTag()
	log.Debug("Starting response stream to:", i.PeerID(), tag)
	err := i.Peer.SendMessage(&Packet{
		PacketType: PType_Response,
		Namespace:  tag,
		Payload:    value,
	})

	if err != nil {
		log.Error("Failed to respond")
	}
	i.totalReplies++
}

func (i *Inbound) EndReply() {
	tag := i.Message.returnTag()
	log.Debug("Ending response stream to:", i.PeerID(), tag)
	err := i.Peer.SendMessage(&Packet{
		PacketType: PType_ResponseEnd,
		Namespace:  tag,
		Payload:    i.totalReplies,
	})

	if err != nil {
		log.Error("Failed to terminate response")
	}
}

type SatPlug struct {
	Satellite *Satellite

	Inbounds      chan *Inbound
	inOp          noise.Opcode
	registeredSat chan interface{}
	rseKill       map[string]chan interface{}
}

func (b *SatPlug) OnBegin(p *protocol.Protocol, peer *noise.Peer) error {
	id := hex.EncodeToString(protocol.PeerID(peer).(skademlia.ID).PublicKey())

	if _, exists := b.Satellite.Peers[id]; exists {
		log.Error("Peer already connected:", id)
		return protocol.DisconnectPeer
	}

	b.Satellite.SetPeer(id, peer)
	skademlia.WaitUntilAuthenticated(peer)
	log.Infof("%v has connected", id)

	// Setup message receiver killswitch
	b.rseKill[id] = make(chan interface{}, 1)
	go b.ReceiveSatelliteEvents(peer, b.rseKill[id])

	//Bootstrap to s/kad
	peers := skademlia.FindNode(
		b.Satellite.Node,
		protocol.NodeID(b.Satellite.Node).(skademlia.ID),
		skademlia.BucketSize(),
		8)
	log.Infof("Bootstrapped to the s/kad network with %v peers", len(peers))

	return nil
}

func hexify(pids []skademlia.ID) []string {
	var a []string
	for _, id := range pids {
		a = append(a, hex.EncodeToString(id.PublicKey()))
	}
	return a
}

func (b *SatPlug) ReceiveSatelliteEvents(peer *noise.Peer, kill chan interface{}) {
	log.Sub(log_inbound).Infof("rse starting")
	for {
		select {
		case <-kill:
			log.Sub(log_inbound).Infof("rse terminated")
			return

		case msg := <-peer.Receive(b.inOp):
			log.Sub(log_inbound).Info("Received Inbound: ", msg.(Packet).PacketType)
			b.Inbounds <- &Inbound{
				Peer:    peer,
				Message: msg.(Packet),
				Payload: msg.(Packet).Payload,
			}
		}
	}
}

func (b *SatPlug) OnEnd(p *protocol.Protocol, peer *noise.Peer) error {
	log.Info("Disconnecting peer")
	id := hex.EncodeToString(protocol.PeerID(peer).(skademlia.ID).PublicKey())
	b.rseKill[id] <- 1
	return nil
}

func (b *SatPlug) OnRegister(p *protocol.Protocol, node *noise.Node) {
	b.inOp = noise.RegisterMessage(noise.NextAvailableOpcode(), (*Packet)(nil))
	log.Sub(log_inbound).Debugf("Message Opcode: %v", b.inOp)
}

func (b *SatPlug) ProcessSatelliteEvents() {
	// wait for a satellite to be registered to start processing the satellite events
	<-b.registeredSat
	log.Sub(log_inbound).Info("Event Processor started")
	for in := range b.Inbounds {
		eventSig := fmt.Sprintf("%v/%v", in.Message.PacketType, in.Message.Namespace)
		ev, exists := b.Satellite.Events[eventSig]
		if exists {
			log.Debug("calling event sig: ", eventSig)
			go ev(in)
		} else {
			log.Error("Received foreign event signature: ", eventSig)
		}

	}
}

func (b *SatPlug) RegisterSatellite(s *Satellite) {
	b.Satellite = s
	b.registeredSat <- 1
}

func NewInboundProcessor() *SatPlug {
	c := make(chan *Inbound, 1000)
	plug := SatPlug{
		Inbounds:      c,
		inOp:          0,
		registeredSat: make(chan interface{}),
		rseKill:       make(map[string]chan interface{}),
	}

	go plug.ProcessSatelliteEvents()
	return &plug
}
