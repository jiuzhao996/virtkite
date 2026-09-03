#!/bin/bash
cd ~/vmops
nohup ./vmops > vmops.log 2>&1 &
echo $! > vmops.pid
echo "vmops started with PID: $(cat vmops.pid)"
sleep 2
curl -s http://localhost:8080/api/health