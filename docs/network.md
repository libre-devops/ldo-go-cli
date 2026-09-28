# Proxies and certificates

[Back to the docs](README.md)

Behind a corporate proxy, `ldo-go` works with the usual settings, and every HTTPS call it makes
goes the same way as the Azure CLI it runs, so both behave alike.

```bash
ldo-go network test                                  # the proxy, the certificates, each service
ldo-go network test --url https://intranet.corp.example/health
LDO_PROXY_ADDRESS=127.0.0.1:3129 ldo-go network test  # try a proxy before setting it anywhere
```

`network test` asks Entra ID, Graph, Azure Resource Manager and Defender (in the profile's
cloud), and each ServiceNow instance configured, for something they answer without a
sign-in, and expects a 2xx (Defender, which has nothing to show without a token, answers
401). It shows the proxy each call went through, which certificates are trusted, and when a
call fails, what to try. It exits 3 when any fails.

## The proxy

Each call takes the first of these that applies:

| | Setting | For |
| --- | --- | --- |
| 1 | this machine, and `169.254.169.254` | never a proxy: sign-in redirects to localhost, managed identity |
| 2 | `no_proxy` in the config file, and `NO_PROXY` | hosts that go direct, e.g. `.corp.example,10.0.0.0/8` |
| 3 | `LDO_PROXY_ADDRESS` | `ldo-go` alone, without changing `HTTPS_PROXY` for everything else |
| 4 | `proxy` in the config file | the same, kept in the file |
| 5 | `HTTPS_PROXY`, `HTTP_PROXY`, `ALL_PROXY` | the usual variables, which most tools read |

The operating system's own proxy setting (Windows and macOS) is not read, unlike the Python
`ldo`: name the proxy in one of the ways above.

An address without a scheme, such as `127.0.0.1:3128`, means `http://`. Whichever proxy
applies, the Azure CLI gets it too, as `HTTPS_PROXY` and `NO_PROXY`.

An address may carry a user and password (`http://alice:secret@proxy.corp.example:8080`),
for a proxy that takes a plain one. It is used as it is but never shown: `ldo-go network test`,
its JSON and every message write `alice:***@`. A local cntlm or Px is still the better way,
since then no password sits in your environment at all.

```toml
proxy = "127.0.0.1:3128"
no_proxy = ".corp.example, 10.0.0.0/8"
```

### A proxy that wants NTLM or Kerberos

Corporate proxies often want a sign-in of their own, which a 407 from `network test` shows.
Neither `ldo-go` nor the Azure CLI speaks NTLM, so run a small local proxy that does, and point
`ldo-go` at it:

- [cntlm](https://cntlm.sourceforge.net/): set `Username`, `Domain` and `Proxy` in
  `cntlm.conf`, then `cntlm -H` for the password hashes and `cntlm -I -M
  https://graph.microsoft.com` to test them. It listens on `127.0.0.1:3128` unless its
  `Listen` line says otherwise.
- [Px](https://github.com/genotrance/px): the same on Windows, signing in as you with no
  password stored.

```bash
export LDO_PROXY_ADDRESS=127.0.0.1:3129    # wherever cntlm or Px listens
ldo-go network test
```

When nothing is set and a call cannot get out, `network test` looks for something listening
on 3128 or 3129 and suggests it. A 407 from a local proxy means it could not sign in
upstream: check its credentials.

Automatic proxy scripts (PAC and WPAD files) are not read: name the proxy instead.

## Certificates

A TLS-inspecting proxy re-signs every site with its own certificate. By default `ldo-go`
trusts, together, so that it works where IT has installed that certificate on the machine:

- the operating system's store: the Windows certificate store, the macOS keychains, or the
  Linux system bundle, which Go verifies against as the platform does;
- and any the config file's `ca_bundle` adds.

```toml
ca_bundle = "~/certs/corp-root.pem"   # the proxy's root, when it is not in the OS store
```

The Azure CLI, which `ldo-go` runs for `azure-cli` profiles, trusts only the one file a
`REQUESTS_CA_BUNDLE` names, so with a `ca_bundle` it is handed a file of the machine's
certificates and `ca_bundle`'s together, written to your cache folder (`~/.cache/ldo` on
Linux, `~/Library/Caches/ldo` on macOS, `%LOCALAPPDATA%\ldo` on Windows). Both then trust the
same things.

To use one bundle exactly as it is instead, nothing added, name it with `LDO_CA_BUNDLE`
(or the standard `REQUESTS_CA_BUNDLE` or `CURL_CA_BUNDLE`):

```bash
export LDO_CA_BUNDLE=/etc/corp/ca-bundle.pem
```

When a certificate does not verify, `network test` names who issued it: a proxy's own name
(Zscaler, Netskope, a company CA) shows it is inspecting traffic. It says, too, when a server
answers `https` in plain HTTP, which a proxy set up for the wrong port does.
