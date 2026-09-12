# Quickstart

Build, set up a vault, crawl from the command line, and start the
application. Details are in [README.md](../README.md) and
[BUILDING.md](BUILDING.md).

## Build

```bash
./scripts/build.sh              # crawl, omvault, mapview -> build/bin/
./scripts/build-app.sh          # the application -> build/app/omegamaps
./scripts/build-app.sh --list   # if it cannot find Qt: what it can see, and why not
```

## Vault

One vault holds SSH and SNMP credentials. It lives at `~/.omegamaps/vault.json`,
and the command-line tools and the application share it. Secrets are always
prompted, never passed as arguments.

```bash
omvault init                                       # choose a master password (8+ characters)

omvault add -name lab -user cisco -tag lab         # SSH password
omvault add -name lab-key -user cisco -key ~/.ssh/id_ed25519 -passphrase
omvault add -name lab-agent -user cisco -auth agent
omvault add -name lab-ro -auth snmp-v2c -tag lab   # prompts for the community
omvault add -name lab-v3 -auth snmp-v3 -user monitor \
    -snmp-auth-proto SHA256 -snmp-priv-proto AES   # prompts for auth key, then privacy key

omvault list                                       # names, types, tags; never secrets
omvault keyring set                                # unlock from the OS keyring from now on
omvault keyring status
```

Other ways to supply the master password: a prompt (the default), or
`OMEGAMAPS_VAULT_PASSWORD` in the environment. `OMEGAMAPS_NO_KEYRING=1` skips
the keyring for one run.

Narrow a credential to part of the network with `-scope-domain`,
`-scope-cidr` or `-scope-platform`; order credentials with `-priority`.

## Crawl from the command line

```bash
# SNMP first, SSH where SNMP fails; record the run
crawl -vault ~/.omegamaps/vault.json -methods snmp,ssh \
    -seed 172.16.1.2 -domain lab.local -depth 5 \
    -events lab.events.jsonl -o lab.json

# one device only, to check credentials and reachability
crawl -vault ~/.omegamaps/vault.json -methods snmp,ssh -seed 172.16.1.2 -depth 0 -v

# a seed facing other organisations: map their routers, never log into them
crawl -vault ~/.omegamaps/vault.json -methods snmp,ssh \
    -seed 10.0.0.1 -domain example.net -allow-domain example.net \
    -exclude linux,phone -o site.json

mapview -map lab.json            # view it; exports PNG, JSON, draw.io
                                 # (or omegamaps-viewer lab.json, from the Qt build)
```

| Often needed | |
|---|---|
| `-concurrency 20` | devices collected at once (default 5) |
| `-cred-tag lab` | use only credentials tagged `lab` |
| `-known-hosts ./lab_known_hosts` | keep a lab's host keys out of `~/.ssh/known_hosts` |
| `-legacy` | older SSH algorithms, for older IOS and NX-OS |
| `-jump admin@bastion` | reach devices through a bastion |
| `-v` | log every decision |

No vault: `-user` with `-key` or `-password` for SSH, and
`OMEGAMAPS_SNMP_COMMUNITY` (or `OMEGAMAPS_SNMP_V3_USER`, `_AUTH`, `_PRIV`,
`_AUTHPROTO`, `_PRIVPROTO`) for SNMP.

## Start the application

```bash
omegamaps                                    # the crawl form
omegamaps --theme dark                       # cyber, dark or light; remembered
omegamaps lab.events.jsonl --map lab.json    # replay a recording (default 10x)
omegamaps lab.events.jsonl --map lab.json --speed 1    # real time; 0 is instant
```

At startup the application unlocks `~/.omegamaps/vault.json` from the OS
keyring or `OMEGAMAPS_VAULT_PASSWORD` if it can, and never prompts. Otherwise:

1. **Credentials** -- Unlock (or Create, for a first vault); tick *Remember in
   the OS keyring* to skip this next time. Add credentials here or with
   `omvault`.
2. **Connection** -- seeds, domain suffix, and *Dial only under* when a seed
   faces other networks.
3. **Discovery options** -- *Collect with* "SNMP, then SSH" for most networks.
4. **Output** -- directory and map name.
5. **Start crawl**, or **Test single** to try the first seed alone.

Every crawl writes `name.json`, `name.events.jsonl` and `name.log` to the
output directory. When it finishes, choosing a speed at the top replays it;
**Open recording…** replays any other. The form is remembered between runs.