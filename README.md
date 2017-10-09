# Distributed-Monitor

Z założenia rozproszony monitor umożliwiający synchronizację rozproszonych procesów, w praktyce rozproszona kolejka udostępniająca żądania `get` i `put` (wyłącznie inty).

Do implementacji posłużyły algorytm konsensusu [Raft](https://raft.github.io/) oraz ZeroMQ w wersji 4.2.2.
