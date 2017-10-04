#!/bin/bash

kill_jobs()
{
	echo "CTRL-C received. Killing all jobs."
	kill $(jobs -p)
	exit $?
}

if [ $# -lt 2 ]; then
	echo "Usage: `basename $0` jobname number"
	exit 1
fi

cmd=$1
n=$2

for i in `seq 1 $n`; do
	$cmd &
done

trap kill_jobs SIGINT

while true; do read x; done
