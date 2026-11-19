# pgprotopaginate

PostgreSQL cursor and offset pagination, filtering, sorting, and result counts
for Go APIs using Protocol Buffers and pgx.

Use `paginate.ProtoPaginate` with a pgx connection, pool, or transaction and a
function that converts database models to protobuf messages.

## Install

```sh
go get github.com/otpki/pgprotopaginate/paginate
```

Protobuf types come from [Buf Schema Registry](https://buf.build/omnitrust/pgprotopaginate)
as a Go dependency. No local code generation is required.

## Filtering

- Separate filters use AND. Multiple values in one equality filter match any value.
- Groups support AND and OR, with the same validation and value types as ordinary filters.
- Invalid filters return errors.
- Nested messages and maps require explicit `filterable` or `sortable` annotations
  and matching database columns. Nested field paths are not supported.

Pagination-info requests check the database model. For standalone metadata, use
`GetPaginationInfoForModel[Proto, Model]()`; `GetPaginationInfo[Proto]()` checks only
the protobuf schema.

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
