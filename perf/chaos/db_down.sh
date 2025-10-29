#!/usr/bin/env bash
# Simulasikan DB down (iptables drop/stop container), verifikasi /health/ready=503 dan cache path tetap melayani katalog.
echo 'simulate db down ...'
