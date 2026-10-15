# ztewifi

Command line tool that reads signal, network and device information from a
**ZTE portable WiFi** hotspot (RedCap Mobile Hotspot; tested with an `MU3650` /
market name `F40` running `BD_CNMU3650V1.0.0B08`) through the device's built-in
web API, and can also toggle the wireless radio of the hotspot itself.

It is the backend of the LuCI application
[`luci-app-ztewifi`](https://github.com/hakureiyuyuko/luci-app-ztewifi), which
is also shipped in the ImmortalWrt feeds.

## Usage

```sh
ztewifi status --human        # human readable summary
ztewifi status                # JSON (default)
ztewifi test                  # check reachability and login, prints {ok,message}
ztewifi config                # effective configuration (password masked)
ztewifi set --ip 192.168.0.1 --password xxx --interval 5 --enabled 1
ztewifi wifi status           # read the WiFi switch of the device
ztewifi wifi on | off         # turn the device WiFi on / off
ztewifi raw cmd1,cmd2         # debug: read arbitrary goform commands
ztewifi version
```

Options `--ip` and `--password` override the UCI configuration for a single
invocation; `ZTEWIFI_IP` / `ZTEWIFI_PASSWORD` environment variables do the same.

## Configuration

Read from `/etc/config/ztewifi` (UCI):

```
config ztewifi 'main'
	option enabled '1'
	option ip '192.168.0.1'
	option password ''
	option interval '5'
	option timeout '8'
```

## How it talks to the device

The device exposes a `goform` web API used by its own web UI. The details below
were reverse engineered from the device firmware:

* every request needs `Referer: http://<ip>/` and
  `X-Requested-With: XMLHttpRequest`, otherwise the device answers with an
  empty body - and the connection is HTTP/1.0 style, so keep-alives must be
  disabled
* login: read the `LD` challenge, then
  `password = SHA256(SHA256(plain) + LD)` (uppercase hex), POSTed as
  `goformId=LOGIN`; the reply carries a `Set-Cookie: zsidn=<hex>` session which
  must be sent with every further request
* write operations additionally need an `AD` field (the device asks for it
  whenever `ACCESSIBLE_ID_SUPPORT` is set, which is the case on this model):
  `AD = SHA256(SHA256(wa_inner_version + cr_version) + RD)`. Without it the
  device happily replies `{"result":"success"}` but does not apply anything.
* reads are batched through
  `GET /goform/goform_get_cmd_process?isTest=false&multi_data=1&cmd=a,b,c`
* the WiFi switch uses `goformId=switchWiFiModule&SwitchOption=0` to turn the
  radio off and `goformId=switchWiFiChip&ChipEnum=<chip1_2|chip1|chip2>&GuestEnable=0`
  to turn it back on.

## Building

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ztewifi .
```

There are no dependencies outside the Go standard library.

## License

MIT
