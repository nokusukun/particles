package main

import (
	"encoding/hex"
	"flag"
	"io/ioutil"
	"os"

	"github.com/perlin-network/noise/skademlia"

	"github.com/nokusukun/particles/keys"
	"github.com/nokusukun/particles/roggy"
)

type Params struct {
	OutFile string
	ReadKey bool
}

var parameters = new(Params)
var log = roggy.Printer("pkgen")

func init() {
	flag.StringVar(&parameters.OutFile, "f", "mykey.key", "destination file name")
	flag.BoolVar(&parameters.ReadKey, "r", false, "read key")
	flag.Parse()
}

func main() {

	if parameters.ReadKey {
		readKeys(parameters.OutFile)
		return
	}

	log.Info("Generating Keys....")
	newkeys := skademlia.RandomKeys()
	kb, err := keys.Serialize(newkeys)
	if err != nil {
		panic(err)
	}

	log.Infof("New key generated: %v", hex.EncodeToString(newkeys.PublicKey()))
	err = ioutil.WriteFile(parameters.OutFile, kb, os.ModePerm)
	if err != nil {
		panic(err)
	}

	log.Infof("Key saved to: %v", parameters.OutFile)
}

func readKeys(file string) {
	log.Infof("Reading Key: %v", file)

	b, err := ioutil.ReadFile(file)
	if err != nil {
		log.Error("Failed to read key", err)
		panic(err)
	}

	k, err := keys.Deserialize(b)
	if err != nil {
		log.Error("Failed to deserialize key", err)
		panic(err)
	}

	log.Infof("Public Key: %v", hex.EncodeToString(k.PublicKey()))
	roggy.Wait()
}
