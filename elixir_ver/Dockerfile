FROM elixir:1.17-otp-27-alpine AS builder

WORKDIR /app

RUN mix local.hex --force && mix local.rebar --force

COPY mix.exs mix.lock* ./
RUN MIX_ENV=prod mix deps.get --only prod

COPY config config
RUN MIX_ENV=prod mix deps.compile

COPY lib lib
COPY priv priv
RUN MIX_ENV=prod mix compile && MIX_ENV=prod mix release

FROM elixir:1.17-otp-27-alpine AS runtime

WORKDIR /app

COPY --from=builder /app/_build/prod/rel/rc_notification ./

EXPOSE 8080

CMD ["./bin/rc_notification", "start"]
