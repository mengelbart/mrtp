# MRTP

## Setup SCReAM Gstreamer plugin

* Clone with submodules
* In root directory, set `GST_PLUGIN_PATH=./scream/gstscream/target/debug/` and `LD_LIBRARY_PATH=./scream/code/wrapper_lib/`

## Setup gopipe
### h264
Installation encoder:
* Mac: brew install x264
* Ubuntu: apt install libx264-dev

Installation decoder:
* install libav


### vpx
Installation:
* Mac: brew install libvpx
* Ubuntu: apt install libvpx-dev
