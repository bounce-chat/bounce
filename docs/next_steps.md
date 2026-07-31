# Next Steps

Bounce started as an experiment to see if a fully featured group chat application was feasible using a totally flat architecture inside of a mixnet.  The biggest questions were around the network latency and mobile battery consumption.  While Tor occasionally gets overloaded, latency has not been an issue otherwise.  Mobile battery consumption is certainly noticeable, and while it is an issue for some devices, many users have reported they are able to get through their whole day with comfortable battery to spare.  With these encouraging results, Bounce development will continue with the following priorities:

## Stability and Bug Fixes

The primary goal right now is to get Bounce tested on a wide set of devices with real-world behaviors, and to work through any bugs that come up.

## New Features

A few basic features remain, most importantly:

* emoji reactions
* replies
* editing messages
* link previews
* mentions
* fine-grained notification control

These will all be taken on next, along with a few other Bounce-specific chat features that are planned.

## Performance

There is likely a good amount of performance optimization left to do, particularly in the UI.  Some profiling and benchmarking is required.

### Storage

I want to separate data storage from the chat engine using an interface, and experiment with alternative non-relational approaches to storing Bounce's data, particularly [pebble](https://github.com/cockroachdb/pebble).

### Battery Optimization

For Android users who own another device that is always online, and who don't care about real-time notification, I want to experiment with scheduled startups of the Bounce service, and see if it makes a significant difference on battery usage.  For example, Bounce on Android could shut down after the user leaves the application, and start up in the background every 5 or 10 (or however many) minutes to sync with a sync device.  When paired with encrypted devices, this might be an acceptable way to use Bounce on more battery-limited devices.

## Replacing Tor

Bounce wouldn't be here without bootstrapping on Tor, but the eventual goal is to self-host a mixnet made up of Bounce users.

Originally I started [go-i2p](https://github.com/hkparker/go-i2p) with the goal of hosting Bounce on i2p.  I pivoted to Tor in order to begin the development of Bounce more quickly, but go-i2p development has been taken up by others [here](https://github.com/go-i2p/go-i2p).  I'm curious to experiment with I2P again and see how it performs, but while using a mixnet that is shared by many applications has privacy benefits, these networks carry a lot of bittorrent traffic, which can slow down the network and be a nuisance for a low-latency chat app.  If Bounce ever has enough users to support it's own network, I plan to create a new network and transition devices over to it.
