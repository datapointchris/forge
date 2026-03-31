#!/bin/bash

if [ -f ".pre-commit-config.yaml" ]; then
  echo "found .pre-commit-config.yaml"
  exit 0
fi

if [ -f ".pre-commit-config.yml" ]; then
  echo "found .pre-commit-config.yml"
  exit 0
fi

echo "no .pre-commit-config found"
exit 1
