#!/bin/bash

if [ ! -d ".planning" ]; then
  exit 2
fi

docs=$(ls .planning/)

if [ -z "$docs" ]; then
  exit 2
fi

echo "$docs"
