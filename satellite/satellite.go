package satellite

import (
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/perlin-network/noise"
	"github.com/perlin-network/noise/cipher/aead"
	"github.com/perlin-network/noise/handshake/ecdh"
	"github.com/perlin-network/noise/protocol"
	"github.com/perlin-network/noise/skademlia"

	"github.com/nokusukun/particles/config"
	"github.com/nokusukun/particles/roggy"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

const (
	ResponseStreamBuffer   = 100
	ResponseStreamLifetime = 10 * time.Second
)

var log = roggy.Printer("Satellite")

type SatEvent func(i *Inbound)

type Satellite struct {
	Node             *noise.Node
	InboundProcessor *SatPlug
	Peers            map[string]*noise.Peer
	Events           map[string]SatEvent

	pMap *sync.RWMutex
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
	//params.NAT = nat.NewUPnP()

	node, err := noise.NewNode(params)
	if err != nil {
		panic(err)
	}

	satPlug := NewInboundProcessor()
	sat := &Satellite{Node: node, InboundProcessor: satPlug}
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
	log.Infof("s/kad ID: %v", hex.EncodeToString(protocol.NodeID(node).(skademlia.ID).PublicKey()))

	// Makes sure that everything else gets initialized before the plug starts processing events
	satPlug.RegisterSatellite(sat)

	return sat
}

type ResponseStream struct {
	// Tag is the ResponseStream tag used to register the Respond and RespondEnd events in the satellite
	Tag    string
	Stream chan *Inbound
	// Done is a channel which returns an int if the ResponseStream ended. It doesn't say if the ResponseStream
	//      ended with an error or failed mid way though
	Done chan interface{}

	// lets the timeout goroutine know that the request has been peacefully responded with
	timeoutStop chan interface{}

	// WHY ALL OF THIS ADDITIONAL FLUFF?
	// Apparently, ResponseStream.close() gets executed while some of the packets are being sent through the channel
	// creating the 'channel is closing' panics.
	// --
	// hasEnded stops ResponseStream.close() from continuing if all of the packets hasn't arrived yet
	// endPacketCount is the amount of Response packets, this value is sent by the remote peer as a payload
	//                on the RespondEnd packet. Value is -1 if the endPacket hasn't arrived yet
	// packetCount is the amount of Response packets received by the ResponseStream
	hasEnded       chan interface{}
	endPacketCount int
	packetCount    int

	// closing limits the ResponseStream.close() to only run once
	// terminated indicates if the response stream is truly dead
	// onClose runs right before ResponseStream.Stream gets closing
	closing    bool
	terminated bool
	onClose    func(stream *ResponseStream)
}

const (
	StreamEndOK = iota
	StreamEndError
	StreamEndTimeout
	StreamEndNotImplemented
)

func (r *ResponseStream) close() {
	if !r.closing || !r.terminated {
		// Set close flag to true to ensure that this never gets called again
		r.closing = true

		// Wait for all of the expected packets to arrive
		// send int to hasEnded if you want to close whenever
		endType := <-r.hasEnded

		// Run the onClose function
		r.onClose(r)

		// Close the main response stream
		close(r.Stream)

		// Signal that the response is done
		r.Done <- endType

		// Stop the timeout goroutine
		r.timeoutStop <- 1
		r.terminated = true
		log.Debug(roggy.Clr("RESPONSE STREAM Terminated ", 1), r.Tag)
	}
}

func (r *ResponseStream) IsClosed() bool {
	return r.terminated
}

// Request sends a request packet to the destination peer, responses are served through
// a `ResponseStream.Stream` channel, the channel closes if the response stream is considered finished.
// Receiving a value from the `ResponseStream.Done` channel also indicates the same thing as a closing `Stream` channel.
//      A response stream may close for other reasons such as the global timeout indicated by `ResponseStreamLifetime`
func (s *Satellite) Request(peer *noise.Peer, namespace string, value interface{}) (*ResponseStream, error) {
	msg, responseStream, err := s.assembleRequest(namespace, value, false)
	if err != nil {
		return nil, err
	}

	// Dispatch an event listener to end the responseStream after the remote peer is done with responding.
	s.Event(PType_ResponseEnd, msg.returnTag(), func(i *Inbound) {
		log.Debugf("Ending request stream by remote, expected/arrived packets: %v/%v", i.Payload, responseStream.packetCount)
		responseStream.endPacketCount = int(i.Payload.(float64))

		// Check if all of the packets have arrived
		if responseStream.endPacketCount == responseStream.packetCount {
			log.Debugf(roggy.Clr("All of the %v packets have arrived", 2), msg.returnTag())
			responseStream.hasEnded <- StreamEndOK
		}

		responseStream.close()
	})

	// Send the request packet to the remote peer
	err = peer.SendMessage(msg)
	if err != nil {
		log.Debug("Ending request stream by error")
		responseStream.hasEnded <- StreamEndError
		responseStream.close()
		return nil, fmt.Errorf("failed to send request: %v", err)
	}

	// Dispatch a timeout goroutine
	go func() {
		select {
		case <-responseStream.timeoutStop:
			return
		case <-time.After(ResponseStreamLifetime):
			log.Debug("Ending request stream by timeout")
			responseStream.hasEnded <- StreamEndTimeout
			responseStream.close()
		}
	}()

	return responseStream, nil
}

func (s *Satellite) assembleRequest(namespace string, value interface{}, isBroadcast bool) (Packet, *ResponseStream, error) {
	msg := Packet{
		PacketType: PType_Request,
		Namespace:  namespace,
		Payload:    value,
	}

	responseStream := &ResponseStream{
		Tag:            msg.returnTag(),
		Stream:         make(chan *Inbound, ResponseStreamBuffer),
		Done:           make(chan interface{}, 1),
		timeoutStop:    make(chan interface{}, 1),
		packetCount:    0,
		endPacketCount: -1,
		hasEnded:       make(chan interface{}, 1),
		closing:        false,
		onClose: func(stream *ResponseStream) {
			s.RemoveEvent(PType_ResponseEnd, msg.returnTag())
			s.RemoveEvent(PType_Response, msg.returnTag())
		},
	}

	// Dispatch an event listener to stream incoming data into a channel
	s.Event(PType_Response, msg.returnTag(), func(i *Inbound) {
		if !responseStream.IsClosed() {
			// Send data to the response stream
			responseStream.Stream <- i
			responseStream.packetCount++

			// check if the respondEnd count has arrived and that if this is the last response
			if responseStream.endPacketCount != -1 && responseStream.endPacketCount == responseStream.packetCount {
				log.Debugf("All of the %v packets have arrived", msg.returnTag())
				responseStream.hasEnded <- 1
			}
		} else {
			log.Error("received response on a closing response stream: %v", msg.returnTag())
		}
	})

	s.Event(PType_NotImplemented, msg.returnTag(), func(i *Inbound) {
		log.Errorf("Request %v is not implemented by the remote peer", namespace)
		responseStream.hasEnded <- StreamEndNotImplemented
		responseStream.close()
	})

	return msg, responseStream, nil
}
