package main

import (
	"bufio"
	"encoding/hex"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/boltdb/bolt"
	"github.com/perlin-network/noise/skademlia"
	"github.com/rs/zerolog"

	"github.com/nokusukun/particles/config"
	"github.com/nokusukun/particles/keys"
	"github.com/nokusukun/particles/roggy"
	"github.com/nokusukun/particles/satellite"
)

var log = roggy.Printer("particled")
var csat = config.Satellite{}
var cdae = config.Daemon{}

func init() {
	flag.UintVar(&csat.Port, "port", 3000, "Listen for peers in specified port")
	flag.StringVar(&csat.Host, "host", "127.0.0.1", "Listen for peers in this host")

	flag.StringVar(&cdae.DialTo, "dial", "", "Bootstrap s/kad from this peer")
	flag.StringVar(&cdae.ApiListen, "api", "", "Enable the api and serve to this address")
	flag.StringVar(&cdae.DatabasePath, "dbpath", "", "Database Path")
	flag.StringVar(&cdae.KeyPath, "key", "", "Read/write key from/to path")
	flag.BoolVar(&cdae.GenerateNewKeys, "generate", false, "Generate new keys")
	flag.BoolVar(&cdae.ShowHelp, "h", false, "Show help")
	flag.Parse()

	if cdae.ShowHelp {
		flag.Usage()
		os.Exit(0)
	}

	zerolog.SetGlobalLevel(zerolog.Disabled)
	roggy.LogLevel = 5
}

func main() {
	log.Info("Starting Particle Daemon")

	// database initialization
	db, err := bolt.Open(cdae.DatabasePath, os.ModePerm, nil)
	if err != nil {
		log.Error("Opening database failed")
		panic(err)
	}

	// satellite bootstrapping
	keyPair, err := getKeys(cdae.KeyPath)
	if err != nil {
		log.Error("Failed to get keyPair:", err)
		log.Error("Your key might not exist, try with the -generate flag")
	}
	sat := satellite.BuildNetwork(&csat, keyPair)

	if cdae.DialTo != "" {
		log.Info("Connecting s/kad bootstrap at ", cdae.DialTo)
		_, err := sat.Node.Dial(cdae.DialTo)
		if err != nil {
			log.Errorf("Failed to dial to s/kad bootstrap")
		}
	}
	bootstrapEvents(sat, db)

	// API
	if cdae.ApiListen != "" {
		log.Info("Starting API on:", cdae.ApiListen)
		router := generateAPI(sat)
		log.Error(http.ListenAndServe(cdae.ApiListen, router))
	} else {
		log.Info("No API port provided")
	}

	defer func() {
		err := db.Close()
		if err != nil {
			log.Error("failed to close database", err)
		}
		log.Info("Killing node...")
		sat.Node.Kill()
		roggy.Wait()
	}()

	select {}
}

func getKeys(path string) (*skademlia.Keypair, error) {
	_, err := os.Stat(path)
	if err == nil {
		log.Notice("-generate flag specified but key already exists, using that instead")
	}

	if cdae.GenerateNewKeys && err != nil {

		log.Info("Generating new keys...")
		newkeys := skademlia.RandomKeys()
		kb, err := keys.Serialize(newkeys)
		if err != nil {
			panic(err)
		}

		if path == "" {
			reader := bufio.NewReader(os.Stdin)
			fmt.Print("Enter key filename: ")
			path, err = reader.ReadString('\n')
			if err != nil {
				panic(err)
			}
		}

		log.Infof("New key generated: %v", hex.EncodeToString(newkeys.PublicKey()))
		err = ioutil.WriteFile(path, kb, os.ModePerm)
		if err != nil {
			panic(err)
		}

		log.Infof("Key saved to: %v", path)
	}

	return keys.ReadKeys(path)
}
