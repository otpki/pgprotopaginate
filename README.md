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

- Separate filters use AND, including repeated queries on the same column.
- EQUALS and NOT_EQUALS require exactly one value. IN and NOT_IN take nonempty lists of "alternatives".
- Ordered comparisons require one value, BETWEEN requires two. Null and empty queries take no values.
- Scalar false, zero, and empty strings use the declared protobuf field type.
- Groups support AND and OR, with the same validation and value types as ordinary filters.
- Invalid filters return errors.
- Mark singular messages `nested = true` to expose paths such as `details.score`.
- Use `column` to map fields to database columns, including table aliases.
- Fields are filterable and sortable by default unless explicitly disabled.

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
