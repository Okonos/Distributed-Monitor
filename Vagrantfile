# -*- mode: ruby -*-
# vi: set ft=ruby :

Vagrant.configure(2) do |config|
	config.vm.box = "ubuntu/xenial64"
	config.vm.provision :shell, path: "provision/bootstrap.sh"
	(1..3).each do |i|
		config.vm.define "monitor_vm#{i}" do |node|
			node.vm.hostname = "vm#{i}"
			node.vm.network "private_network", ip: "192.168.2.#{i+1}"
			# set default routing to private network
			node.vm.provision :shell, run: "always", inline: <<-SHELL
				ip route del default
				ip route add default via 192.168.2.1 dev enp0s8
			SHELL
		end
	end
end
