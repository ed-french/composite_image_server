#!/bin/bash

# Check if URL was provided
if [ -z "$1" ]; then
  echo "Usage: $0 <url> <comport> <interval>"
  exit 1
fi

URL="$1"

COMPORT="$2"
INTERVAL=$3

stty -F $COMPORT 115200 cs8 -cstopb -parenb

while true; do
    TIMESTAMP=$(date +"%m-%d/%H:%M")
    # Try to fetch the URL quietly
    wget --spider -q "$URL"



    # Check the exit status of wget
    if [ $? -eq 0 ]; then
    echo "$TIMESTAMP $URL = UP"
    echo "$TIMESTAMP $URL = UP" > $COMPORT
    else
    echo "$TIMESTAMP $URL = DOWN" > $COMPORT
    echo "$TIMESTAMP $URL = UP"
    fi
    sleep $INTERVAL
done