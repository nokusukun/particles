package satellite

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/perlin-network/noise"
	"github.com/perlin-network/noise/payload"
)

type PType int

const (
	PType_Internal PType = iota
	PType_Message
	PType_Broadcast

	PType_Request
	PType_Response
	PType_ResponseEnd
)

type Packet struct {
	PacketType PType       `json:"p"`
	Namespace  string      `json:"ns"`
	Payload    interface{} `json:"c"`
	Timestamp  int64       `json:"ts"`

	_retTag string
}

func (p Packet) returnTag() string {
	if p.Timestamp == 0 {
		p.Timestamp = time.Now().Unix()
	}
	if p._retTag == "" {
		b, err := json.Marshal(p)
		if err != nil {
			log.Error("failed to generate return tag: ", err, p)
		}

		sha256encoder := sha256.New()
		sha256encoder.Write(b)
		p._retTag = base64.StdEncoding.EncodeToString(sha256encoder.Sum([]byte("")))
	}

	return p._retTag
}

func (p Packet) Read(reader payload.Reader) (noise.Message, error) {
	b, err := reader.ReadBytes()
	if err != nil {
		log.Error("failed to read packet ", err)
		return nil, err
	}

	err = json.Unmarshal(b, &p)
	if err != nil {
		log.Error("failed to unmarshal packet ", err)
		return nil, err
	}

	return p, nil
}

func (p Packet) Write() []byte {
	if p.Timestamp == 0 {
		p.Timestamp = time.Now().Unix()
	}
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	// return b
	return payload.NewWriter(nil).
		WriteBytes(b).
		Bytes()
}
