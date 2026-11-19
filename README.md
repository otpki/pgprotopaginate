# pgprotopaginate

PostgreSQL pagination, filtering, and sorting for Go APIs, powered by Protocol Buffers and pgx.

Supports cursor and offset pagination, filtering, sorting, and result counts.
Use `paginate.ProtoPaginate` with a pgx connection, pool, or transaction and a
function that converts database models to protobuf messages.

## Install

```sh
go get github.com/otpki/pgprotopaginate/paginate
```

Protobuf types come from [Buf Schema Registry](https://buf.build/omnitrust/pgprotopaginate)
as a Go dependency. No local code generation is required.

## Development

With Go and [Just](https://github.com/casey/just) installed:

```sh
just tools
just check
```

Run the full test suite against PostgreSQL:

```sh
docker compose up -d --wait
just test
docker compose down
```

## License

[Apache License 2.0](LICENSE).
