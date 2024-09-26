package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fiatjaf/eventstore"
	"github.com/fiatjaf/khatru"
	"github.com/fiatjaf/khatru/policies"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip10"
	"github.com/coreos/go-systemd/activation"
)

//go:embed template/index.html
var indexHTML string

//go:embed template/assets
var assets embed.FS

var (
	version string
)

type Config struct {
	RelayName        string   `toml:"relay_name"`
	RelayDescription string   `toml:"relay_description"`
	RelayURL         string   `toml:"relay_url"`
	RelayPort        string   `toml:"relay_port"`
	RelayContact     string   `toml:"relay_contact"`
	RelayIcon        string   `toml:"relay_icon"`
	OwnerPubkeys     []string `toml:"owner_pubkeys"`
	DBPath           string   `toml:"db_path"`
	RefreshInterval  int      `toml:"refresh_interval"`
	MinFollowers     int      `toml:"min_followers"`
	FetchSync        bool     `toml:"fetch_sync"`
	BackupRelays     []string `toml:"backup_relays"`
}

var pool *nostr.SimplePool
var wdb nostr.RelayStore
var rootNotesList *RootNotes
var relays []string
var config Config
var trustNetwork []string
var oneHopNetwork []string
var trustNetworkMap map[string]bool
var pubkeyFollowerCount = make(map[string]int)
var trustedNotes uint64
var untrustedNotes uint64
var relay *khatru.Relay

func main() {
	nostr.InfoLogger = log.New(io.Discard, "", 0)
	magenta := "\033[91m"
	gray := "\033[90m"
	reset := "\033[0m"

	art := magenta + `
   ____ _                     _      _      
  / ___| |__  _ __ ___  _ __ (_) ___| | ___ 
 | |   | '_ \| '__/ _ \| '_ \| |/ __| |/ _ \
 | |___| | | | | | (_) | | | | | (__| |  __/
  \____|_| |_|_|  \___/|_| |_|_|\___|_|\___|` + gray + `
                        powered by Khatru
` + reset

	fmt.Println(art)

	configFile := flag.String("config-file", "config.toml", "Path to the TOML configuration file")
	flag.Parse()

	log.Println("🚀 Booting up Chronicle relay")
	relay := khatru.NewRelay()
	ctx := context.Background()
	pool = nostr.NewSimplePool(ctx)
	config = LoadConfig(*configFile)

	relay.Info.Name = config.RelayName
	relay.Info.PubKey = config.OwnerPubkeys[0] // Use the first pubkey as the main one
	relay.Info.Icon = config.RelayIcon
	relay.Info.Contact = config.RelayContact
	relay.Info.Description = config.RelayDescription
	relay.Info.Software = "https://github.com/dtonon/chronicle"
	relay.Info.Version = version

	for _, pubkey := range config.OwnerPubkeys {
		appendPubkey(pubkey)
	}

	db := getDB()
	if err := db.Init(); err != nil {
		panic(err)
	}
	wdb = eventstore.RelayWrapper{Store: &db}

	rootNotesList = NewRootNotes("db/root_notes")
	if err := rootNotesList.LoadFromFile(); err != nil {
		fmt.Println("Error loading strings:", err)
		return
	} else {
		log.Println("🗣️  Monitoring", rootNotesList.Size(), "threads")
	}

	relay.RejectEvent = append(relay.RejectEvent,
		policies.RejectEventsWithBase64Media,
		policies.EventIPRateLimiter(5, time.Minute*1, 30),
	)

	relay.RejectFilter = append(relay.RejectFilter,
		policies.NoEmptyFilters,
		policies.NoComplexFilters,
	)

	relay.RejectConnection = append(relay.RejectConnection,
		policies.ConnectionRateLimiter(10, time.Minute*2, 30),
	)

	relay.StoreEvent = append(relay.StoreEvent, db.SaveEvent)
	relay.QueryEvents = append(relay.QueryEvents, db.QueryEvents)
	relay.DeleteEvent = append(relay.DeleteEvent, db.DeleteEvent)
	relay.RejectEvent = append(relay.RejectEvent, func(ctx context.Context, event *nostr.Event) (bool, string) {
		if acceptedEvent(*event, true) {
			addEventToRootList(*event)
			propagateToBackupRelays(*event)
			return false, ""
		}
		return true, "event not allowed"
	})

	// WoT and archiving procedures
	var wg sync.WaitGroup
	wg.Add(1) // We expect one goroutine to finish
	interval := time.Duration(config.RefreshInterval) * time.Hour
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	go func() {
		refreshProfiles(ctx)
		refreshTrustNetwork(ctx, relay)
		wg.Done()
		for {
			if config.FetchSync {
				archiveTrustedNotes(ctx, relay)
			}
			<-ticker.C // Wait for the ticker to tick
			refreshProfiles(ctx)
			refreshTrustNetwork(ctx, relay)
		}
	}()

	// Wait for the first execution to complete
	wg.Wait()

	mux := relay.Router()

	serverRoot, fsErr := fs.Sub(assets, "template/assets")
	if fsErr != nil {
		log.Fatal(fsErr)
	}
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(serverRoot))))
	mux.Handle("/favicon.ico", http.FileServer(http.FS(serverRoot)))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl := template.Must(template.New("index").Parse(indexHTML))
		data := struct {
			RelayName        string
			RelayDescription string
			RelayURL         string
			RelayPort        string
			OwnerPubkeys     []string
		}{
			RelayName:        config.RelayName,
			RelayDescription: config.RelayDescription,
			RelayURL:         config.RelayURL,
			RelayPort:        config.RelayPort,
			OwnerPubkeys:     config.OwnerPubkeys,
		}
		err := tmpl.Execute(w, data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	log.Println("🎉 Relay ready")
	
	listeners, err := activation.Listeners()
	if err != nil {
		log.Fatal(err)
	}

	if len(listeners) > 0 {
		// Use systemd socket activation
		log.Println("Using systemd socket activation")
		err = http.Serve(listeners[0], mux)
	} else {
		// Fallback to regular port binding
		log.Println("Fallback: running on port", config.RelayPort)
		err = http.ListenAndServe(":"+config.RelayPort, mux)
	}

	if err != nil {
		log.Fatal(err)
	}
}

func LoadConfig(configFile string) Config {
	var config Config
	_, err := toml.DecodeFile(configFile, &config)
	if err != nil {
		log.Fatalf("Error decoding config file: %v", err)
	}

	log.Println("📝 Set refresh interval to", config.RefreshInterval, "hours")
	log.Println("📝 Set minimum followers to", config.MinFollowers)
	log.Println("📝 Fetch sync:", config.FetchSync)

	return config
}

func propagateToBackupRelays(event nostr.Event) {
	for _, relayURL := range config.BackupRelays {
		go func(url string) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := pool.Publish(ctx, url, event)
			if err != nil {
				log.Printf("Failed to propagate event to backup relay %s: %v", url, err)
			}
		}(relayURL)
	}
}

func acceptedEvent(event nostr.Event, findRoot bool) bool {
	for _, ownerPubkey := range config.OwnerPubkeys {
		if event.PubKey == ownerPubkey {
			// If is a reply check that the thread has been archived
			rootReference := nip10.GetThreadRoot(event.Tags)
			if findRoot &&
				rootReference != nil && // It's a reply
				!rootNotesList.Include(rootReference.Value()) { // It's not archived
				go fetchConversation(rootReference)
			}
			return true
		}
	}

	if belongsToValidThread(event) && belongsToWotNetwork(event) {
		return true
	}

	return false
}

func fetchConversation(eTag *nostr.Tag) {
	ctx := context.Background()
	timeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	eventID := eTag.Value()
	eventRelay := eTag.Relay()

	go func() {
		filters := []nostr.Filter{
			{
				IDs: []string{eventID},
			},
			{
				Kinds: []int{
					nostr.KindArticle,
					nostr.KindDeletion,
					nostr.KindReaction,
					nostr.KindZapRequest,
					nostr.KindZap,
					nostr.KindTextNote,
				},
				Tags: nostr.TagMap{"e": []string{eventID}},
			},
		}

		for ev := range pool.SubMany(timeout, append([]string{eventRelay}, seedRelays...), filters) {
			wdb.Publish(ctx, *ev.Event)
		}
	}()

	<-timeout.Done()
}

func belongsToValidThread(event nostr.Event) bool {

	eReference := nip10.GetThreadRoot(event.Tags)
	if eReference == nil {
		// We already accept root notes by owner
		return false
	}

	if event.Kind == nostr.KindTextNote ||
		event.Kind == nostr.KindArticle {

		rootCheck := rootNotesList.Include(eReference.Value())
		return rootCheck
	}

	// The event refers to a note in the thread
	if event.Kind == nostr.KindDeletion ||
		event.Kind == nostr.KindReaction ||
		event.Kind == nostr.KindZapRequest ||
		event.Kind == nostr.KindZap {

		ctx := context.Background()
		_, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		filter := nostr.Filter{
			IDs: []string{eReference.Value()},
		}
		eventChan, _ := wdb.QueryEvents(ctx, filter)
		for range eventChan {
			return true
		}
	}

	return false

}

func addEventToRootList(event nostr.Event) {

	// Add only notes and articles to the root list
	if event.Kind != nostr.KindTextNote &&
		event.Kind != nostr.KindArticle {
		return
	}

	rootReference := nip10.GetThreadRoot(event.Tags)
	var rootReferenceValue string
	if rootReference == nil { // Is a root post
		rootReferenceValue = event.ID
	} else { // Is a reply
		rootReferenceValue = rootReference.Value()
	}
	rootNotesList.Add(rootReferenceValue)
}
