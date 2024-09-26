![image](logo.png)

Chronicle is a personal relay for [Nostr](https://njump.me), built on the [Khatru](https://khatru.nostr.technology) framework, that stores complete conversations in which the owners have taken part and nothing else: pure signal.  
This is possible since writing is limited to the threads in which the owners have participated (either as original posters or with replies/zaps/reactions), and only to their trusted network (WoT), to protect against spam.

Chronicle fits well in the Outbox model, so you can use it as your read/write relay, and it also automatically becomes a space-efficient backup relay.

## How it works

Every incoming event is verified against some simple rules.  
If it's signed by one of the relay owners, it is automatically accepted.  
If it is posted by someone else, it is checked if it is part of a conversation in which an owner participated *and* if the author is in the owners' social graph (to the 2nd degree). If both conditions are met, it is accepted; otherwise, it is rejected.

If an event published by an owner refers to a conversation that is not yet known by the relay, it tries to fetch it.

## Features highlight

- Works nicely as inbox/outbox/dm relay
- Space-efficient backup relay
- Offers spam protection by WoT
- Permits loading old notes with a "fetch sync" option
- Supports multiple relay owners
- Uses systemd socket activation for improved deployment flexibility
- Propagates events to backup relays

## Configure

After cloning the repo, create a `config.toml` file based on the example provided in the repository and personalize it:

```toml
# Relay Information
relay_name = "My Chronicle Relay"
relay_description = "A personal Chronicle relay"
relay_url = "wss://my-relay.example.com"
relay_port = "8080"
relay_contact = "operator@example.com"
relay_icon = "https://example.com/icon.png"

# Owner Information
owner_pubkeys = [
  "pubkey1",
  "pubkey2",
  "pubkey3"
]

# Database Configuration
db_path = "./db"

# Web of Trust Configuration
refresh_interval = 24
min_followers = 3
fetch_sync = false

# Backup Relays
backup_relays = [
  "wss://backup1.example.com",
  "wss://backup2.example.com"
]
```

## Build and Run

Build it with `go install` or `go build`, then run it with:

```
./chronicle --config-file=config.toml
```

By default, Chronicle uses [Badger](https://github.com/dgraph-io/badger) as event storage since it makes it easier to cross-compile.  
You can also use [lmdb](https://www.symas.com/lmdb), compiling with:
```
go build -tags=lmdb .
```

## Systemd Socket Activation

To use systemd socket activation:

1. Create a systemd socket file (e.g., `/etc/systemd/system/chronicle.socket`):

```
[Unit]
Description=Chronicle Nostr Relay Socket

[Socket]
ListenStream=8080

[Install]
WantedBy=sockets.target
```

2. Create a systemd service file (e.g., `/etc/systemd/system/chronicle.service`):

```
[Unit]
Description=Chronicle Nostr Relay
Requires=chronicle.socket

[Service]
ExecStart=/path/to/chronicle --config-file=/path/to/config.toml
User=chronicle
Group=chronicle

[Install]
WantedBy=multi-user.target
```

3. Enable and start the socket:

```
sudo systemctl enable chronicle.socket
sudo systemctl start chronicle.socket
```

4. Start the service:

```
sudo systemctl start chronicle.service
```

The relay will now use systemd socket activation for improved deployment flexibility.

## Credits

Chronicle uses some code from the great [wot-relay](https://github.com/bitvora/wot-relay).

## License

This project is licensed under the MIT License.
