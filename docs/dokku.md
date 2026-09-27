# Dokku / Herokuish

The HTTP/SSO daemon can scale. **The Discord gateway cannot** — two processes with the same bot token kick each other off the gateway, duplicate slash-command handling, and fight over role sync.

Split them with a Procfile:

| Process | Command | Scale |
|---------|---------|-------|
| `web` | HTTP, `/ws/sso`, login splice, web admin, REST share DMs | 1+ |
| `discord` | Discord gateway (slash commands, role events) | **1** |

## Buildpacks

This repo includes `.buildpacks` (Heroku Go) so Dokku prefers **herokuish** over the Compose `Dockerfile`. After the first deploy with this Procfile, Dokku only starts **web** until you scale Discord:

```bash
dokku ps:scale APP web=1 discord=1
```

If `discord` stays at 0, slash commands and role sync stop. Do this immediately after deploy.

`web` listens on `$PORT`. Keep `DISCORD_ENABLED=true` in shared config; only the `discord` process opens the gateway.

If herokuish cannot build Go 1.26, set the Dockerfile builder and keep the same Procfile:

```bash
dokku builder:set APP selected dockerfile
dokku ps:scale APP web=1 discord=1
```

The image `CMD` is `-process all` (Compose). Dokku runs Procfile commands instead (`daemon -process web` / `daemon -process discord`). The Dockerfile must not use `ENTRYPOINT`, or Procfile lines are passed as extra args.

## Scaling notes

- **discord=1** always.
- **web>1** is OK for independent GUI sockets. Presence/metrics are per-web-process (in memory), so “who is online” is not global across web dynos.
- Share DMs use Discord **REST** from `web` (no second gateway).

## Local Compose

Compose still runs one container as `-process all` (HTTP + gateway together).
