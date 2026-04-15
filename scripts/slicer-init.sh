
#!/bin/bash

export GOPATH=$HOME/go/
export PATH=$PATH:/usr/local/go/bin:$GOPATH/bin

#install Go
arkade system install go

#install Ollama
curl -fsSL https://ollama.com/install.sh | sh
ollama login

#install tailscale
sudo chattr -i /etc/resolv.conf
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
