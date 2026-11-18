# Install anyscale on Linux

You need systemd, `curl`, and root access.

## Do this

1. Stop the regular Tailscale service so it does not compete for routes and
   DNS:

   ```sh
   sudo systemctl disable --now tailscaled
   ```

2. Build and install Anyscale from the repository root, then start the
   daemon:

   ```sh
   make && sudo make install
   sudo systemctl enable --now anyscaled
   ```

3. Log in to your first tailnet:

   ```sh
   sudo anyscalectl ts login
   ```

   Open the login link it prints. anyscale creates a profile named `default`.

4. Check it:

   ```sh
   anyscalectl status
   anyscalectl ts status
   ```

If `default` is listed and the second command shows the tailnet, you are done.
To connect another tailnet, run `sudo anyscalectl profiles add work` and then
`sudo anyscalectl ts -p work login`.

The systemd unit enables `anyscaled` at boot and starts it. It keeps
state in `/var/lib/anyscale` and runtime sockets in `/run/anyscale`.

## If something goes wrong

Check the service, then follow its logs:

```sh
sudo systemctl status anyscaled
sudo journalctl -u anyscaled -f
```

Restart it after fixing the problem:

```sh
sudo systemctl restart anyscaled
```

## Updates

```sh
anyscalectl update status
sudo anyscalectl update check
sudo anyscalectl update apply
```

Automatic stable-release updates are enabled by default. The daemon checks
after a randomized startup delay and then daily, verifies the release checksum,
switches `anyscale` and `anyscaled` together, and restarts itself. No systemd timer
is installed. Use `sudo anyscalectl update disable` to opt out and
`sudo anyscalectl update enable` to turn automatic updates back on.

## Change daemon flags

Most users can skip this section. Run:

```sh
sudo systemctl edit anyscaled
```

Add a drop-in such as:

```ini
[Service]
Environment="ANYSCALED_FLAGS=-synthetic-pool=10.250.0.0/16 -verbose"
```

Then apply it:

```sh
sudo systemctl restart anyscaled
```

The service already sets the state path and socket directory. Development and
packaging options are documented in [DEVELOPMENT.md](../DEVELOPMENT.md).
