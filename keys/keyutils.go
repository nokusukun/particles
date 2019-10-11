package keys

import (
	"encoding/json"
	"io/ioutil"

	"github.com/perlin-network/noise/skademlia"
)

type KeyExport struct {
	PK []byte
	C1 int
	C2 int
}

func Serialize(keys *skademlia.Keypair) ([]byte, error) {
	return json.Marshal(KeyExport{
		PK: keys.PrivateKey(),
		C1: keys.C1,
		C2: keys.C2,
	})
}

func Deserialize(b []byte) (*skademlia.Keypair, error) {
	var k KeyExport
	err := json.Unmarshal(b, &k)
	if err != nil {
		return nil, err
	}

	return skademlia.LoadKeys(k.PK, k.C1, k.C2)
}

func ReadKeys(keyPath string) (*skademlia.Keypair, error) {
	if b, err := ioutil.ReadFile(keyPath); err != nil {
		return nil, err
	} else {
		return Deserialize(b)
	}
}
