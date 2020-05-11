# Steps to Deploy DC with gRPC update

### Written By Prerit Oberai

## Environment Setup

### Master Node
1. Install Docker and Kubernetes and Go (v1.14)
2. Install cmake
3. Install cpprestsdk
    ```
    sudo apt-get update
    sudo apt-get install g++ git libboost-atomic-dev libboost-thread-dev libboost-system-dev libboost-date-time-dev libboost-regex-dev libboost-filesystem-dev libboost-random-dev libboost-chrono-dev libboost-serialization-dev libwebsocketpp-dev openssl libssl-dev ninja-build
    git clone https://github.com/Microsoft/cpprestsdk.git casablanca
    cd casablanca
    mkdir build.debug
    cd build.debug
    cmake -G Ninja .. -DCMAKE_BUILD_TYPE=Debug
    ninja
    sudo ninja install
    ```
4. Install Protobuf Compiler
    ```
    sudo apt-get install autoconf automake libtool curl make g++ unzip
    git clone https://github.com/protocolbuffers/protobuf.git
    cd protobuf
    git submodule update --init --recursive
    ./autogen.sh
    ./configure
    sudo make
    sudo make check
    sudo make install
    sudo ldconfig # refresh shared library cache.
    ```
5. Install libgrpc++
    - Note: You may need to change the `DCMAKE_INSTALL_PREFIX` flag to a directory for yuor local filesystem
    ```
    $ sudo apt install -y build-essential autoconf libtool pkg-config
    $ git clone --recurse-submodules -b v1.28.1 https://github.com/grpc/grpc
    $ cd grpc
    $ mkdir -p cmake/build
    $ pushd cmake/build
    $ cmake -DgRPC_INSTALL=ON \
         -DgRPC_BUILD_TESTS=OFF \
         -DCMAKE_INSTALL_PREFIX=/usr/local/ \
         -DCMAKE_BUILD_TYPE=Release       \
         -DBUILD_SHARED_LIBS=ON \
         ../..
    $ make -j
    $ make install
    $ popd
    $ sudo ldconfig //This was for protobuf, I needed it though..
    ```

### Worker Nodes
1. Build, compile and boot into the appropriate EC-4.20.16 Kernel version
    ```
    cp -v /boot/config-$(uname -r) .config
    sudo apt-get install build-essential libncurses-dev bison flex libssl-dev libelf-dev
    make menuconfig
    sudo make -j$(nproc) && sudo make -j$(nproc) modules_install && sudo make -j$(nproc) install
    sudo reboot
    ```
2. Install Docker and Kubernetes and Go (v1.14)
3. 

## Run and deploy DC
1. Create a K8s Cluster
2. Run Agent on the worker nodes
    - To run Agent:
        ```
        go run main.go
        ```
        - Note: If you get an error with `panic: invalid configuration: [unable to read client-cert /var/lib/kubelet/pki/kubelet-client-current.pem for default-auth due to open /var/lib/kubelet/pki/kubelet-client-current.pem: permission denied`, you might need to change the user permissions for the /var/lib/kubectl directory. Not a good way to solve this, but work in progress.. i.e. `sudo chmod -R 777 /var/lib/kubelet/`

    - Prerit's Note: why does the agent need the kube config file? 

3. Build & GCM on the master node
    - To build GCM:
        ```
        cmake -DCMAKE_BUILD_TYPE=Release -DCMAKE_C_COMPILER=/usr/bin/gcc -DCMAKE_CXX_COMPILER=/usr/bin/g++ .
        sudo make
        ```
    - To run GCM:
        ```
        ./ec_gcm tests/app_def.json
        ```

4. Deploy Pods on Master Node
    - To run Deployer: 
        ```
        go run main.go -f app_deploy.json
        ```
        Note: If you see the error: `fatal: could not read Username for 'https://github.com': terminal prompts disabled`, then you need to do the following to solve it:
        - `git config --global --add url."git@github.com:".insteadOf "https://github.com/"`

        
<!--Run Deployer on the master node
    - Create a namespace that the application will be deployed to - this should match up with the namespace string in your app_def.json file
        - to create namespace, use: `kubectl create ns <namespace>`
    - Update app_def.json to point to the correct path for the application and the GCM/Agent IPs 
    - To run Deployer: 
        ```
        go run main.go -f app_deploy.json
        ```
        Note: If you see the error: `fatal: could not read Username for 'https://github.com': terminal prompts disabled`, then you need to do the following to solve it:
        - `git config --global --add url."git@github.com:".insteadOf "https://github.com/"`
    - To clean up all pods in a namespace, delete the namespace via: `kubectl delete ns <namespace>`
-->
<!-- ## Setup Steps:
1. Install Docker and Kubernetes and Go
2. Instantiate a K8s cluster
3. Install python3-pip, asyncio, and aiohttp
4. Install k8s client go libraries:
    ```
        go get k8s.io/api
        go get k8s.io/apimachinery
        go get k8s.io/client-go
    ``` -->