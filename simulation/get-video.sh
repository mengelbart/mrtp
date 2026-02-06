#!/bin/bash

if [ ! -f "Johnny_1280x720_60.y4m" ]; then
    wget https://media.xiph.org/video/derf/y4m/Johnny_1280x720_60.y4m
else
    echo "Johnny_1280x720_60.y4m already exists."
fi