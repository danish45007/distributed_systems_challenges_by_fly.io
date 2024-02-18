#!/bin/bash

cwd=$(pwd)
go build -o bin
../maelstrom/maelstrom test -w unique-ids --bin $cwd/bin --time-limit 30 --rate 1000 --node-count 3 --availability total --nemesis partition
cd $cwd