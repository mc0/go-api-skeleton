# go-api-skeleton

- Install brew via https://brew.sh
- Install golang 1.8 via `brew install go`
- Install protoc via `brew install protobuf protobuf-c`
- Install protoc-gen-go via `go get -u github.com/golang/protobuf/protoc-gen-go`
- Include `$GOPATH/bin` in your `$PATH`

## Building a new API

- Replace all instances of "go-api-skeleton" with the API name
- Look in Makefile for `repopath` and correct where this is pointing to
  correspond to the proper import path/repo.
- Edit k8s/deployment.yaml to correspond to the new kubernetes deployment
  or add new files for templating ({IMAGE} is replaced via `make template`).

## Production

### Building

```
make binary
make build
make template
```

## Development

### Database

- Set up a mysql server

    ```
    docker run -d -e MYSQL_ROOT_PASSWORD=test -p 3306:3306 mysql
    ```
- Connect to your database and run the following:

    ```
    CREATE DATABASE skeleton;
    CREATE TABLE `object` (
      `id` int(11) unsigned NOT NULL AUTO_INCREMENT,
      `name` varchar(255) NOT NULL DEFAULT '',
      PRIMARY KEY (`id`)
    ) ENGINE=InnoDB AUTO_INCREMENT=1 DEFAULT CHARSET=utf8;
    INSERT INTO object VALUES (1, "1234Test");
    ```
- Set the following environment variables to correspond to your server:

    ```
    export MYSQL_USERNAME=root
    export MYSQL_PASSWORD=test
    export MYSQL_HOSTNAME=127.0.0.1
    export MYSQL_PORT=3306
    export MYSQL_DATABASE=test
    ```

### Building

- Build the protobuf file if necessary: `make proto`
- Build the app locally: `make binary`

### Running

```
./go-api-skeleton
```

### Debugging

- Run the app
- Install npm for debugging via `brew install npm`
- Install grpcc for debugging via `npm install -g grpcc`
- Run the following:

    ```
    grpcc --proto proto/skeleton.proto -s Skeleton -i -a localhost:24601
    ```
- Run the following in grpcc:

    ```
    let res = client.getObject({id: 1}, pr);
    ```

# License

MIT
