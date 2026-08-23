ARG BASE_IMAGE=homeloom-backend
FROM ${BASE_IMAGE}

USER root
COPY homeloom /usr/local/bin/homeloom
COPY homeloom-mcp-agent /usr/local/bin/homeloom-mcp-agent
USER homeloom
