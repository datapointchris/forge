#!/bin/bash

if [ ! -d ".planning" ]; then
  echo "no .planning directory"
  exit 2
fi

docs=$(ls .planning/)

if [ -z "$docs" ]; then
  echo ".planning is empty"
  exit 2
fi

echo "$docs"
