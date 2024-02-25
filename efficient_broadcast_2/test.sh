#!/bin/bash

cwd=$(pwd)
go build -o bin
../maelstrom/maelstrom test -w broadcast --bin $cwd/bin --node-count 25 --time-limit 20 --rate 100 --latency 100
cd $cwd