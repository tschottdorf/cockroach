#!/bin/bash

rm out.0 out.1
./run.sh 1
./run.sh 0
benchstat out.0 out.1 | tee result.txt
