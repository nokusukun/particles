package satellite

import (
	"fmt"
	"sync"
	"time"

	"github.com/perlin-network/noise"
	"github.com/perlin-network/noise/skademlia"

	"github.com/nokusukun/particles/roggy"
)

type CStreamReturn int

const (
	StreamEndOK CStreamReturn = iota
	StreamEndError
	StreamEndTimeout
	StreamEndNotImplemented
)

type ResponseStream struct {
	// Tag is the ResponseStream tag used to register the Respond and RespondEnd events in the satellite
	Tag    string
	Stream chan *Inbound
	// Done is a channel which returns an int if the ResponseStream ended. It doesn't say if the ResponseStream
	//      ended with an error or failed mid way though
	Done chan CStreamReturn

	// lets the timeout goroutine know that the request has been peacefully responded with
	timeoutStop chan CStreamReturn

	// WHY ALL OF THIS ADDITIONAL FLUFF?
	// Apparently, ResponseStream.close() gets executed while some of the packets are being sent through the channel
	// creating the 'channel is closing' panics.
	// --
	// hasEnded stops ResponseStream.close() from continuing if all of the packets hasn't arrived yet
	// endPacketCount is the amount of Response packets, this value is sent by the remote peer as a payload
	//                on the RespondEnd packet. Value is -1 if the endPacket hasn't arrived yet
	// packetCount is the amount of Response packets received by the ResponseStream
	hasEnded       chan CStreamReturn
	endPacketCount int
	packetCount    int
	packetIDs      []string
	pidLock        *sync.RWMutex

	// closing limits the ResponseStream.close() to only run once
	// terminated indicates if the response stream is truly dead
	// onClose runs right before ResponseStream.Stream gets closing
	closing    bool
	terminated bool
	onClose    func(stream *ResponseStream)
}

func (s *ResponseStream) ingestPID(message *Packet) {
	go func() {
		s.pidLock.Lock()
		s.packetIDs = append(s.packetIDs, message.ReturnTag())
		s.pidLock.Unlock()
	}()
}

func (s *ResponseStream) hasPID(newPid string) bool {
	for _, pid := range s.packetIDs {
		if pid == newPid {
			return true
		}
	}
	return false
}

// Assembles the request, registering the receiver events and whatnot
// NOTE: DO NOT EVER MODIFY THE RETURNED MESSAGE
func (s *Satellite) assembleRequest(packetType PType, namespace string, value interface{}, isBroadcast bool) (Packet, *ResponseStream, error) {
	msg := Packet{
		PacketType: packetType,
		Namespace:  namespace,
		Payload:    value,
	}

	rs := &ResponseStream{
		Tag:            msg.ReturnTag(),
		Stream:         make(chan *Inbound, ResponseStreamBuffer),
		Done:           make(chan CStreamReturn, 1),
		timeoutStop:    make(chan CStreamReturn, 1),
		packetCount:    0,
		packetIDs:      []string{},
		pidLock:        &sync.RWMutex{},
		endPacketCount: -1,
		hasEnded:       make(chan CStreamReturn, 1),
		closing:        false,
		onClose: func(stream *ResponseStream) {
			s.RemoveEvent(PType_ResponseEnd, msg.ReturnTag())
			s.RemoveEvent(PType_Response, msg.ReturnTag())
		},
	}

	// Dispatch an event listener to stream incoming data into a channel
	s.Event(PType_Response, msg.ReturnTag(), func(i *Inbound) {
		if !rs.IsClosed() {
			// TODO-1: implement DoS protection for seek operations
			// From tod-1: Mitigate multiple responses of the same
			//  messages by keeping track of the return tags
			if rs.hasPID(i.Message.ReturnTag()) {
				log.Debugf("%v already received, disposing", i.Message.ReturnTag())
				return
			}

			// Send data to the response stream
			rs.Stream <- i
			rs.packetCount++
			rs.ingestPID(&i.Message)
			// check if the respondEnd count has arrived and that if this is the last response
			if !isBroadcast && rs.endPacketCount != -1 && rs.endPacketCount == rs.packetCount {
				log.Debugf("All of the %v packets have arrived", msg.ReturnTag())
				rs.hasEnded <- 1
			}
		} else {
			log.Error("received response on a closing response stream: %v", msg.ReturnTag())
		}
	})

	s.Event(PType_NotImplemented, msg.ReturnTag(), func(i *Inbound) {
		log.Errorf("Request %v is not implemented by the remote peer", namespace)
		rs.hasEnded <- StreamEndNotImplemented
		rs.close()
	})

	return msg, rs, nil
}

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

func (s *Satellite) BroadcastAsync(namespace string, value interface{}) {
	s.bcast(namespace, value, true)
}

func (s *Satellite) Broadcast(namespace string, value interface{}) []error {
	return s.bcast(namespace, value, false)
}

func (s *Satellite) bcast(namespace string, value interface{}, async bool) []error {
	msg := Packet{
		PacketType: PType_Broadcast,
		Namespace:  namespace,
		Payload:    value,
	}
	log.Debugf("broadcasting message: %v as %v", msg, msg.ReturnTag())
	if !async {
		return skademlia.Broadcast(s.Node, msg)
	}

	skademlia.BroadcastAsync(s.Node, msg)
	return nil
}

func (s *Satellite) Seek(namespace string, value interface{}) (*ResponseStream, error) {
	msg, responseStream, err := s.assembleRequest(PType_Seek, namespace, value, true)
	if err != nil {
		return nil, err
	}

	s.Event(PType_ResponseEnd, msg.ReturnTag(), func(i *Inbound) {
		log.Debugf("Peer finished seek request %v ", i.PeerID())
	})

	log.Debugf("SEEK: %v", msg.ReturnTag())
	// Send the request packet to the remote peer
	errs := skademlia.Broadcast(s.Node, msg)
	if len(errs) != 0 {
		log.Debug("Ending broadcast prematurely")
		responseStream.hasEnded <- StreamEndError
		responseStream.close()
		return nil, fmt.Errorf("failed to send broadcast: %v", err)
	}

	// Dispatch a timeout goroutine
	go func() {
		select {
		case <-responseStream.timeoutStop:
			return
		case <-time.After(SeekStreamLifetime):
			log.Debug("Ending seek stream by timeout")
			responseStream.hasEnded <- StreamEndTimeout
			responseStream.close()
		}
	}()

	return responseStream, nil
}

// Request sends a request packet to the destination peer, responses are served through
// a `ResponseStream.Stream` channel, the channel closes if the response stream is considered finished.
// Receiving a value from the `ResponseStream.Done` channel also indicates the same thing as a closing `Stream` channel.
//      A response stream may close for other reasons such as the global timeout indicated by `ResponseStreamLifetime`
func (s *Satellite) Request(peer *noise.Peer, namespace string, value interface{}) (*ResponseStream, error) {
	msg, responseStream, err := s.assembleRequest(PType_Request, namespace, value, false)
	if err != nil {
		return nil, err
	}

	// Dispatch an event listener to end the responseStream after the remote peer is done with responding.
	s.Event(PType_ResponseEnd, msg.ReturnTag(), func(i *Inbound) {
		log.Debugf("Ending request stream by remote, expected/arrived packets: %v/%v", i.Payload, responseStream.packetCount)
		responseStream.endPacketCount = int(i.Payload.(float64))

		// Check if all of the packets have arrived
		if responseStream.endPacketCount == responseStream.packetCount {
			log.Debugf(roggy.Clr("All of the %v packets have arrived", 2), msg.ReturnTag())
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
