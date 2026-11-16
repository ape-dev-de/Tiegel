# syntax=docker/dockerfile:1
FROM composer:2 AS composer
FROM --platform=$BUILDPLATFORM php:8.2-fpm-alpine AS base
COPY --from=composer /usr/bin/composer /usr/bin/composer
FROM base AS dev
RUN pecl install xdebug
