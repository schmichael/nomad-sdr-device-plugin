Nomad RTL2838 SDR Device Plugin
===============================

For ID `0bda:2838` Realtek Semiconductor Corp. RTL2838 DVB-T software defined
radio (SDR) devices.

Built from skeleton project for [Nomad device plugins](https://www.nomadproject.io/docs/internals/plugins/devices.html).

Requirements
------------

- [Nomad](https://www.nomadproject.io/downloads.html) 0.9+
- [Go](https://golang.org/doc/install) 1.11 or later (to build the plugin)
- [libusb-1.0-dev](https://libusb.info/)

Building the Plugin
---------------------

```sh
$ git clone git@github.com:schmichael/nomad-sdr-device-plugin.git
$ go build
$ # copy plugin into nomad's -plugin-dir
```

Deploying Device Plugins in Nomad
----------------------

Copy the plugin binary to the
[plugins directory](https://www.nomadproject.io/docs/configuration/index.html#plugin_dir) and
[configure the plugin](https://www.nomadproject.io/docs/configuration/plugin.html) in the client config. Then use the
[device stanza](https://www.nomadproject.io/docs/job-specification/device.html) in the job file to schedule with
device support. (Note, the skeleton plugin is not intended for use in Nomad.)
