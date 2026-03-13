#!/bin/bash

if [ ! -f "Johnny_1280x720_60.y4m" ]; then
    wget https://media.xiph.org/video/derf/y4m/Johnny_1280x720_60.y4m
    concat_file=$(mktemp /tmp/concat_XXXXXX.txt)
    for i in $(seq 1 10); do
        echo "file '$(realpath Johnny_1280x720_60.y4m)'" >> "$concat_file"
    done
    ffmpeg -f concat -safe 0 -i "$concat_file" -c copy Johnny_1280x720_60_tmp.y4m
    rm "$concat_file"
    mv Johnny_1280x720_60_tmp.y4m Johnny_1280x720_60.y4m
else
    echo "Johnny_1280x720_60.y4m already exists."
fi