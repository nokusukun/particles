package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/perlin-network/noise/skademlia"

	"github.com/nokusukun/particles/satellite"
)

type WriteRequest struct {
	PacketType  string      `json:"type"`
	Destination string      `json:"destination"`
	Namespace   string      `json:"namespace"`
	Content     interface{} `json:"content"`
}

func generateAPI(sat *satellite.Satellite) *mux.Router {
	router := mux.NewRouter()

	router.HandleFunc("/peers", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("Retieving Peers")
		var ids []string

		for id, _ := range sat.Peers {
			ids = append(ids, id)
		}

		_ = json.NewEncoder(w).Encode(ids)
	}).Methods("GET")

	router.HandleFunc("/write", func(w http.ResponseWriter, r *http.Request) {
		request := WriteRequest{}

		_ = json.NewDecoder(r.Body).Decode(&request)

		var errCode string
		p, exists := sat.Peers[request.Destination]
		if exists {
			err := p.SendMessage(satellite.Packet{
				PacketType: satellite.PType_Message,
				Namespace:  request.Namespace,
				Payload:    request.Content,
			})
			if err != nil {
				errCode = fmt.Sprintf("failed to write: %v", err)
			}
		} else {
			errCode = fmt.Sprintf("peer does not exist: %v", request.Destination)
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": errCode,
		})
	})

	router.HandleFunc("/broadcast", func(w http.ResponseWriter, r *http.Request) {
		request := WriteRequest{}

		_ = json.NewDecoder(r.Body).Decode(&request)

		var errCode string

		err := skademlia.Broadcast(sat.Node, satellite.Packet{
			PacketType: satellite.PType_Broadcast,
			Namespace:  request.Namespace,
			Payload:    request.Content,
		})
		if err != nil {
			errCode = fmt.Sprintf("failed to write: %v", err)
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": errCode,
		})
	})

	router.HandleFunc("/broadcast_rating", func(w http.ResponseWriter, r *http.Request) {
		rat := Rating{}

		var errCode string
		b, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Debugf("failed to read body: %v", err)
		}

		json.Unmarshal(b, &rat)

		if err != nil {
			log.Debugf("failed to marshal json: %v\n%v", err, string(b))
			errCode = fmt.Sprintf("failed to marshal json: %v", err)
		} else {
			err := skademlia.Broadcast(sat.Node, satellite.Packet{
				PacketType: satellite.PType_Broadcast,
				Namespace:  "new_rating",
				Payload:    rat,
			})
			if err != nil {
				log.Debugf("failed to broadcast: %v", err)
				errCode = fmt.Sprintf("failed to broadcast: %v", err)
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": errCode,
		})
	})

	router.HandleFunc("/ratings/{peer}/{ids}", func(w http.ResponseWriter, r *http.Request) {

		vars := mux.Vars(r)
		var errCode string
		var ratings []*Rating

		p, exists := sat.Peers[vars["peer"]]
		if exists {
			start := time.Now()
			rs, err := sat.Request(p, "get_rating", RatingRequest{vars["ids"]})
			if err != nil {
				errCode = fmt.Sprintf("failed to write: %v", err)
			}
			log.Debug("Waiting for streams")
			for inbound := range rs.Stream {
				ratings = append(ratings, inbound.As(&Rating{}).(*Rating))
			}
			log.Debug("Waiting for streams is complete: ", time.Now().Sub(start))
		} else {
			errCode = fmt.Sprintf("peer does not exist: %v", vars["peer"])
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ratings": ratings,
			"error":   errCode,
		})
	})

	return router
}
