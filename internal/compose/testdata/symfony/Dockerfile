FROM dunglas/frankenphp:1-php8.3 AS frankenphp_upstream
FROM frankenphp_upstream AS frankenphp_base
WORKDIR /app
FROM frankenphp_base AS frankenphp_dev
ENV APP_ENV=dev XDEBUG_MODE=off
