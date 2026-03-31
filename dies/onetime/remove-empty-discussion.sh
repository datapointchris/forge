#!/bin/bash

if [ ! -d ".discussion" ]; then
    exit 2
fi

if [ -z "$(ls -A .discussion)" ]; then
    rm -r .discussion
    echo "removed empty .discussion/"
else
    echo "ERROR: .discussion/ is not empty:"
    ls -la .discussion/
    exit 1
fi
