#!/usr/bin/env bash

apt-get update
apt-get install -y libzmq5
dpkg -i /vagrant/provision/zeromq_4.2.2-1_amd64.deb
ldconfig

# set default routing to private network
ip route del default
ip route add default via 192.168.2.1 dev enp0s8
