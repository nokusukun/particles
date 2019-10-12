## Particles
*I'm using this name just so I can use the term 'particle canon' somewhere in the project*

***NOTE: This is a rewrite of the psfs project, as such a lot of the content you may see here
makes it look like it got ripped out from that repo, because it is***

This project serves as a testbed and framework for a decentralized platform.

The important part is basically `/particles/satellite`, this module handles all of the finding of peers and connection.

### 🌟Features!🌟
* ~~Rendezvous DHT~~ Uses s/kademlia to find peers
* Automated Peer lifecycle handling
* Robust Request and Response streaming
* Broadcasting
* Fully featured event system! (*probably a lie*)


### Events
Particles has several events as defined ~~in the `messages.proto`
document~~
```go
	PType_Internal PType = iota
	PType_Message
	PType_Broadcast

	PType_Request
	PType_Response
	PType_ResponseEnd

	PType_NotImplemented
	PType_Error
```
```
* Internal
    * Used by internal particle events
* Message
    * One way messages
* Broadcast
    * Self explanatory, gets sent to the other peers that are 
    connected to the satellite.

* Request
    * Request packet in which a `Response` packet is expected.
* Response
    * A response packet, used to be in conjunction with the `Request packet`
* ResponseEnd
    * A special response packet which notifies the specific `responseID` that the request is finished and that the
    stream should be closed.

* NotImplemented
    * Indicates that the remote satellite was receieving a not implemented Request
* Error
    * Indicates that there's an error somewhere! 
```

### Event callbacks
Event callbacks lets you handle whatever gets sent your way, think REST API.
All of the events except for Request and Broadcast+Request events doesn't need a response.
```go
	sat.Event(satellite.PType_Message, "hello", func(i *satellite.Inbound) {
		log.Info(i.PeerID(), " said ", i.Payload.(string))
	})
})
```
#### Request Events
Reqeust events are special events where you can respond to the requesting peer
```go
	sat.Event(satellite.PType_Request, "get_rating", func(i *satellite.Inbound) {
		// A pretty ugly oneliner to cast the payload as a struct
		req := i.As(&RatingRequest{}).(*RatingRequest)

		// Signal the requesting peer that there are no more responses left
		// Not responding with EndReply will end up as a timeout for the other peer
		defer i.EndReply()

		// Standard database stuff
		err := db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("ratings"))
			cur := b.Cursor()

			r := []byte(req.Identity)

			for k, v := cur.First(); k != nil; k, v = cur.Next() {
				if bytes.HasPrefix(k, r) || bytes.HasSuffix(k, r) {
					// Unmarshal the data into a Rating struct
					rat := Rating{}
					err := json.Unmarshal(v, &rat)
					if err != nil {
						log.Error("Failed to marshal:", string(k))
						continue
					}
					// Respond to the requesting peer with the Rating struct
					// The remote peer will receive the ratings as a channel stream
					i.Reply(rat)
				}
			}

			return nil
		})

		if err != nil {
			log.Error("failed to respond to request", err)
		}

	})
```

The peer can then request to another peer using the following code
```go
        rs, err := sat.Request(p, "get_rating", RatingRequest{vars["ids"]})
        if err != nil {
            log.Errorf("failed to request: %v", err)
        }
        
        // Everytime the remote peer initiates a `i.Reply`, it gets piped to this channel
        for inbound := range rs.Stream {
            ratings = append(ratings, inbound.Payload)
        }
    
```

### Security
Satellites are inherently secure, connecting requires a 2048bit RSA key in order to interact with each other.
Each packet is signed, but PSFS features a `lazysec` mode where the peers only need to sign the first packet to assume
an authenticated status. Future packets aren't signed afterwards.

Though, there are still several flaws that are still around.