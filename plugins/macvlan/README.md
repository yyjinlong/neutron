# macvlan plugin

## Overview

[macvlan](http://backreference.org/2014/03/20/some-notes-on-macvlanmacvtap/) functions like a switch that is already connected to the host interface.
A host interface gets "enslaved" with the virtual interfaces sharing the physical device but having distinct MAC addresses.
Since each macvlan interface has its own MAC address, it makes it easy to use with existing DHCP servers already present on the network.

## Example configuration

```
{
	"name": "my-macvlan",
	"type": "macvlan",
	"master": "bond0",
	"ipam": {
		"type": "my-ipam"
	}
}
```

## Network configuration reference

* `name` (string, required): the name of the network
* `type` (string, required): "macvlan"
* `master` (string, optional): name of the host interface to enslave. Defaults to default route interace.
* `mode` (string, optional): one of "bridge", "private", "vepa", "passthru". Defaults to "bridge".
* `ipam` (dictionary, required): IPAM configuration to be used for this network. For interface only without ip address, create empty dictionary.
