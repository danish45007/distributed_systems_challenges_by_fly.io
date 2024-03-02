#!/bin/bash

cwd=$(pwd)
go build -o bin
../maelstrom/maelstrom test -w kafka --bin $cwd/bin  --node-count 1 --concurrency 2n --time-limit 20 --rate 1000
cd $cwd