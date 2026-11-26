# Running on AWS

The [README](../README.md#build-and-run) has the short version. This page has the full
setup, the tests from the laptop and the troubleshooting table.

The server runs on an EC2 instance of the AWS Academy Learner Lab, in the same container as
locally: Docker on the instance builds the `Dockerfile` of this folder and runs it with port
8080 published. Nothing in the code is specific to AWS. The server listens on all
interfaces, so it accepts the connections that reach the instance's public IPv4 address, and
the instance's security group decides which clients can reach it.

How the Learner Lab works, and what it means for the server:

- **Region.** The lab allows only `us-east-1` and `us-west-2`, and the ready-made key pair
  `vockey` exists only in `us-east-1`. Use `us-east-1`.
- **Sessions.** *Start Lab* opens a session with a timer, *End Lab* closes it. At the end of
  a session the lab stops the running instances; at the start of the next one it starts them
  again. A console tab left open after the session ended fails with
  `not authorized ... voc-cancel-cred`: open the console again from the lab page.
- **New IP at every session.** A restarted instance gets a new public IPv4 address. Its disk
  is kept, so the cloned repository, the built image and `data/` survive between sessions.
- **Budget.** The lab has a fixed budget. A running instance consumes it; a stopped one costs
  almost nothing.

## One-time setup

**Key.** In the lab page, *AWS Details* → *Download PEM*. The file is the private key of the
`vockey` key pair, needed for SSH. It does not change between sessions, so it is downloaded
once:

```bash
mv ~/Downloads/labsuser.pem ~/.ssh/ && chmod 400 ~/.ssh/labsuser.pem
```

**Instance.** EC2 → *Launch instance*:

| Setting | Value |
|---|---|
| AMI | Amazon Linux 2023, 64-bit (x86) |
| Instance type | `t3.micro` |
| Key pair | `vockey` |
| Auto-assign public IP | Enable |
| Storage | 8 GiB gp3 (default). File systems: None |
| IAM instance profile | `LabInstanceProfile` (a browser terminal through Session Manager if SSH fails) |

**Security group.** Two inbound rules, restricted to the IPv4 addresses that need access:
one rule per address, with `/32` and a description saying whose address it is.

| Type | Port | Source |
|---|---|---|
| SSH | 22 | your IPv4 address |
| Custom TCP | 8080 | your IPv4 address and those of the other users of the server |

The instance has no IPv6 address, so clients reach it over IPv4. The console's *My IP*
option can pick your IPv6 address instead: the rule then never matches, and SSH hangs
without an error. Find your IPv4 address with `curl -4 ifconfig.me`. Rules can be changed at
any time (*Security Groups* → *Edit inbound rules*) and apply at once, without restarting the
instance. Do not leave port 8080 open to `0.0.0.0/0`: anyone could then store files on the
server.

**Docker and git.** Connect to the instance (its public IPv4 address is in the EC2 instance
list):

```bash
ssh -i ~/.ssh/labsuser.pem ec2-user@<public-ip>
```

```bash
sudo dnf install -y docker git
sudo systemctl enable --now docker      # start Docker now and at every boot
sudo usermod -aG docker ec2-user        # use docker without sudo
exit                                    # reconnect: the new group applies to new logins only
```

**Code.** If the repository is private, give the instance a read-only deploy key instead of
personal credentials. It grants access to this repository only, and can be revoked at any
time:

```bash
ssh-keygen -t ed25519 -C "lab01-ec2" -f ~/.ssh/github_deploy -N ""
cat ~/.ssh/github_deploy.pub    # GitHub: repository → Settings → Deploy keys → Add, without write access
printf 'Host github.com\n  IdentityFile ~/.ssh/github_deploy\n  IdentitiesOnly yes\n' >> ~/.ssh/config
chmod 600 ~/.ssh/config
git clone -b Lab01 git@github.com:CristianoCorona/DistributedSystemsLabs.git
```

Replace `Lab01` with the branch to deploy. At the first connection SSH asks to confirm
GitHub's host key: compare the fingerprint with the ones GitHub publishes. For a public
repository no key is needed:
`git clone -b Lab01 https://github.com/CristianoCorona/DistributedSystemsLabs.git`.

## Build and start

On the instance:

```bash
cd ~/DistributedSystemsLabs/lab01
docker build -t http_server .
docker run --rm --name http_server -p 8080:8080 -v "$PWD/data":/app/data http_server
```

The server runs in the foreground: its log is visible, and Ctrl+C stops it. To keep it
running after the SSH session ends, add `-d` after `run`, follow the log with
`docker logs -f http_server` and stop it with `docker stop http_server`. The container has no
restart policy, so it does not start by itself when the instance boots: it is always started
explicitly.

Compared with the local command, `:Z` is left out (it only matters with SELinux, as on
Fedora), and the volume path is absolute, which every Docker version accepts.

## Test from the laptop

Set the instance's address once in each terminal; the commands below use it:

```bash
IP=<public-ip>
S="http://$IP:8080"
```

**File server.**

```bash
curl -i --data-binary 'hello from AWS' "$S/cloud.txt"     # 201 Created
curl -i "$S/cloud.txt"                                   # 200 OK, body "hello from AWS"
curl -i "$S/missing.html"                                # 404 Not Found
curl -i "$S/document.pdf"                                # 400 Bad Request
curl -i -X DELETE "$S/cloud.txt"                         # 501 Not Implemented
curl -i -X FOO "$S/cloud.txt"                            # 400 Bad Request (unknown method)
```

Binary data across the network, unchanged:

```bash
head -c 1000000 /dev/urandom > sample.jpg
curl -s --data-binary @sample.jpg "$S/sample.jpg" > /dev/null
curl -s "$S/sample.jpg" -o out.jpg && cmp out.jpg sample.jpg && echo identical
```

Connection limit: 10 idle connections, then an eleventh request. It is answered only when the
idle connections reach the 5-second header timeout:

```bash
for i in $(seq 10); do (sleep 8 | nc "$IP" 8080 > /dev/null) & done
sleep 0.5
curl -s -o /dev/null -w '%{http_code} after %{time_total}s\n' "$S/missing.html"   # 404 after about 4.5 s
wait
```

**Broker.** Ids and sequence numbers depend on what the server has already handled, so use a
topic name that has not been used yet and read the id from the answer:

```bash
curl -i -X POST "$S/subscribe?topic=cloud"     # 201 Created, body {"id":N}
ID=N                                           # replace N with the id from the answer
```

Long polling, in two terminals (set `IP`, `S` and `ID` in both):

```bash
curl -i "$S/events?topic=cloud&id=$ID"                             # terminal 1: waits
curl -i -X POST --data-binary 'hello' "$S/publish?topic=cloud"     # terminal 2: 200 OK
```

Terminal 1 receives `[{"seq":1,"body":"hello"}]` as soon as terminal 2 publishes. Running the
two terminals on two different machines (for example two team members, both allowed in the
security group) shows the delivery across the network.

Queued messages, timeout and unsubscribe:

```bash
curl -s -X POST --data-binary 'one' "$S/publish?topic=cloud"
curl -s -X POST --data-binary 'two' "$S/publish?topic=cloud"
curl "$S/events?topic=cloud&id=$ID"                      # both messages at once, in order
curl -i "$S/events?topic=cloud&id=$ID"                   # nothing queued: 204 No Content after 15 s
curl -i -X POST "$S/unsubscribe?topic=cloud&id=$ID"      # 200 OK
curl -i "$S/events?topic=cloud&id=$ID"                   # 404 Not Found
```

Clean up on the laptop with `rm -f sample.jpg out.jpg`. The files stored by these commands are
in `data/` on the instance.

## Test scripts on the instance

The test scripts are in the clone but not in the image (`.dockerignore` excludes
`scripts/`). Run them on the instance itself, in a second SSH session, against the running
container:

```bash
cd ~/DistributedSystemsLabs/lab01
bash scripts/broker_test.sh 8080     # last line: Result: 46 passed, 0 failed
(cd scripts && bash atomic.sh)       # only "a 5000000" and "b 5000000" lines
```

The checks are the same as locally (see [testing.md](testing.md)); here they run against the
container that clients reach over the network.

## Update the code

```bash
cd ~/DistributedSystemsLabs/lab01
git pull
docker build -t http_server .
```

Then stop the container and start it again with the same `docker run` command. `data/` is
ignored by git, so `git pull` does not touch the stored files. A container started from an
image built before the pull still runs the old code: for example, an image older than the
broker answers `400` on `/subscribe`, which it treats as a file without an extension.

## End of a session

1. Stop the container: Ctrl+C, or `docker stop http_server`.
2. EC2 → select the instance → *Instance state* → *Stop instance*, and wait for *Stopped*.
   A stopped instance keeps its disk and costs almost nothing. *Terminate* would delete it.
3. Lab page → *End Lab*.

At the next session the lab starts the instance again by itself, and it consumes budget while
it runs. It gets a new public IPv4 address: copy it again from the console. When working from
another network, update the security group rules with the new IPv4 address.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `ssh` hangs, then times out | the rule for port 22 is missing or set to an IPv6 address, or your IPv4 address changed |
| `Permission denied (publickey)` | wrong key, a user other than `ec2-user`, or key permissions not `400` |
| `curl` to port 8080 times out | the rule for port 8080 is missing, or your IPv4 address changed |
| `Connection refused` on port 8080 | the container is not running: check `docker ps -a` |
| `docker: permission denied` | reconnect after `usermod`, or use `sudo docker` |
| Console errors with `voc-cancel-cred` | the lab session ended: *Start Lab*, then open the console from the lab page |
| It worked in the last session, not now | the instance has a new public IP address |
| `400` on `/subscribe` | the image is older than the broker: `git pull` and rebuild |
