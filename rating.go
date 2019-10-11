package main

type Rating struct {
	Source      string      `json:"src"`
	Destination string      `json:"dst"`
	Signature   []byte      `json:"sig"`
	Content     interface{} `json:"rating"`
}

type RatingRequest struct {
	Identity string `json:"ident"`
}
