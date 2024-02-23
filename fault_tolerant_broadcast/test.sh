#!/bin/bash

cwd=$(pwd)
go build -o bin
../maelstrom/maelstrom test -w broadcast --bin $cwd/bin --node-count 5 --time-limit 20 --rate 10 --nemesis partition
cd $cwd