package satellite

import (
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/perlin-network/noise"
	"github.com/perlin-network/noise/cipher/aead"
	"github.com/perlin-network/noise/handshake/ecdh"
	"github.com/perlin-network/noise/nat"
	"github.com/perlin-network/noise/protocol"
	"github.com/perlin-network/noise/skademlia"

	"github.com/nokusukun/particles/config"
	"github.com/nokusukun/particles/roggy"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

const (
	ResponseStreamBuffer   = 100
	ResponseStreamLifetime = 10 * time.Second
	SeekStreamLifetime     = 10 * time.Second
)

var log = roggy.Printer("Satellite")

type SatEvent func(i *Inbound)

// Todo: Implement Satellite.Broadcast, rip out code from the api
// Todo: Implement Satellite.RequestBroadcast
//		Perhaps have some options for lifetime and such?
type Satellite struct {
	Node             *noise.Node
	InboundProcessor *SatPlug
	Peers            map[string]*noise.Peer
	Events           map[string]SatEvent

	bans []string

	pMap *sync.RWMutex
}

func (s *Satellite) BanPeer(peer *noise.Peer) {
	s.pMap.Lock()
	defer s.pMap.Unlock()
	s.bans = append(s.bans, GetPeerID(peer))
}

func (s *Satellite) UnbanPeer(peer *noise.Peer) {
	id := GetPeerID(peer)
	var nb []string
	for _, ban := range s.bans {
		if id == ban {
			continue
		}
		nb = append(nb, ban)
	}

	s.pMap.Lock()
	defer s.pMap.Unlock()
	s.bans = nb
}

func (s *Satellite) IsBanned(peer *noise.Peer) bool {
	id := GetPeerID(peer)
	for _, ban := range s.bans {
		if ban == id {
			return true
		}
	}
	return false
}

func (s *Satellite) SetPeer(id string, peer *noise.Peer) {
	s.pMap.Lock()
	defer s.pMap.Unlock()
	s.Peers[id] = peer
}

func (s *Satellite) Event(eventType PType, namespace string, f SatEvent) {
	eventSig := fmt.Sprintf("%v/%v", eventType, namespace)
	log.Verbose("Registering Event Signature: ", eventSig)
	s.Events[eventSig] = f
}

func (s *Satellite) RemoveEvent(eventType PType, namespace string) {
	eventSig := fmt.Sprintf("%v/%v", eventType, namespace)
	log.Verbose("Removing Event Signature: ", eventSig)
	delete(s.Events, eventSig)
}

func BuildNetwork(config *config.Satellite, keys *skademlia.Keypair) *Satellite {
	log.Info("Initializing Satellite")
	params := noise.DefaultParams()
	params.Keys = keys
	params.Port = uint16(config.Port)
	params.Host = config.Host
	if config.DisableUPNP {
		log.Info("UPnP Disabled")
		params.NAT = nat.NewUPnP()
	}

	node, err := noise.NewNode(params)
	if err != nil {
		panic(err)
	}

	satPlug := NewInboundProcessor()
	sat := &Satellite{Node: node, InboundProcessor: satPlug}
	sat.bans = []string{}
	sat.pMap = &sync.RWMutex{}
	sat.Peers = map[string]*noise.Peer{}
	sat.Events = map[string]SatEvent{}

	protocol.New().
		Register(ecdh.New()).
		Register(aead.New()).
		Register(skademlia.New()).
		Register(satPlug).
		Enforce(node)

	go node.Listen()

	//extIP, err := params.NAT.ExternalIP()
	log.Infof("Listening for remote satellites on port %v.", node.ExternalAddress())
	//log.Infof("NAT External IP: %v:%v", extIP.String(), node.ExternalPort())
	log.Infof("s/kad ID: %v", base32.StdEncoding.EncodeToString(protocol.NodeID(node).(skademlia.ID).PublicKey()))

	// Makes sure that everything else gets initialized before the plug starts processing events
	satPlug.RegisterSatellite(sat)

	return sat
}

func GetPeerID(peer *noise.Peer) string {
	//return base32.StdEncoding.EncodeToString(protocol.PeerID(peer).(skademlia.ID).PublicKey())
	return hex.EncodeToString(protocol.PeerID(peer).(skademlia.ID).PublicKey())
}
