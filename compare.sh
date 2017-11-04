#!/bin/bash

bash badnext2.sh $1
bash badnext2.sh $2
go run cmp/cmp.go $1 $2
