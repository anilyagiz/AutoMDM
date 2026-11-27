# Simulate slow network

The following guide allows the developer/tester to simulate a "slow" connection from AutoMDM to Redis/MySQL.
(It could be used in a similar way to simulate a slow AutoMDM server too.)

The guide assumes you'll build and run the AutoMDM server locally with the `make automdm` and `./build/automdm serve` commands.

(Has been tested on macOS only.)

## 0. Edit docker-compose.yml

Add the following service to the main `docker-compose.yml`:
```yml
  toxiproxy:
    image: shopify/toxiproxy
    ports:
      - "22220:22220"
      - "8474:8474"
```

## 1. Start services

```sh
docker-compose up
```

## 2. Build AutoMDM

```sh
make automdm
```

## 3. Create a new proxy

The following command will create a "slow" proxy to the MySQL server that listens on 22220.

```sh
curl -s -XPOST -d '{"name" : "mysql", "listen" : "toxiproxy:22220", "upstream" : "mysql:3306"}' http://localhost:8474/proxies
{"name":"mysql","listen":"172.30.0.9:22220","upstream":"mysql:3306","enabled":true,"toxics":[]}%
```

## 4. Run team

Run team as usual but connect to MySQL via the proxy created in step (3):
```sh
./build/automdm serve --dev --dev_license --logging_debug --mysql_address localhost:22220 2>&1 | tee ~/automdm.txt
```

## 5. Configure proxy with latency/jitter

Configure 1 second of latency with 500ms of jitter in all DB requests:
```sh
curl -s -XPOST -d '{"type" : "latency", "attributes" : {"latency" : 1000, "jitter": 500}}' http://localhost:8474/proxies/mysql/toxics
{"attributes":{"latency":5000,"jitter":0},"name":"latency_downstream","type":"latency","stream":"downstream","toxicity":1}%
```

<meta name="pageOrderInSection" value="1400">
<meta name="description" value="A guide for simulating slow connections from a local AutoMDM server to a Redis or MySQL database">
