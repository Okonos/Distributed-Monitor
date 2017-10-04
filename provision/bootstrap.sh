#!/usr/bin/env bash

apt-get update
apt-get install -y libzmq5
dpkg -i /vagrant/provision/zeromq_4.2.2-1_amd64.deb
ldconfig
