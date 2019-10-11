package main

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/boltdb/bolt"

	"github.com/nokusukun/particles/satellite"
)

func makeId(source, dest, nextSeq string) []byte {
	return []byte(fmt.Sprint(source, nextSeq, dest))
}

func bootstrapEvents(sat *satellite.Satellite, db *bolt.DB) {
	log := log.Sub("events")

	sat.Event(satellite.PType_Message, "hello", func(i *satellite.Inbound) {
		log.Info(i.PeerID(), " said hello!")
	})

	sat.Event(satellite.PType_Broadcast, "new_rating", func(i *satellite.Inbound) {
		log.Notice("Received Broadcast from", i.PeerID())
		rating := i.As(&Rating{}).(*Rating)
		log.Debug("received broadcast:", rating)
		log.Debug("i.payload:", i.Payload)
		err := db.Update(func(tx *bolt.Tx) error {
			b, err := tx.CreateBucketIfNotExists([]byte("ratings"))
			if err != nil {
				return err
			}

			bRat, err := json.Marshal(rating)
			if err != nil {
				return err
			}

			id, err := b.NextSequence()
			if err != nil {
				return err
			}

			return b.Put(makeId(rating.Source, rating.Destination, string(id)), bRat)
		})

		if err != nil {
			log.Error("failed to ingest", err)
		}
	})

	sat.Event(satellite.PType_Request, "get_rating", func(i *satellite.Inbound) {
		req := i.As(&RatingRequest{}).(*RatingRequest)
		defer i.EndReply()

		err := db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("ratings"))
			cur := b.Cursor()

			r := []byte(req.Identity)

			for k, v := cur.First(); k != nil; k, v = cur.Next() {
				if bytes.HasPrefix(k, r) || bytes.HasSuffix(k, r) {
					rat := Rating{}
					err := json.Unmarshal(v, &rat)
					if err != nil {
						log.Error("Failed to marshal:", string(k))
						continue
					}
					i.Reply(rat)
				}
			}

			return nil
		})

		if err != nil {
			log.Error("failed to respond to request", err)
		}

	})
}
